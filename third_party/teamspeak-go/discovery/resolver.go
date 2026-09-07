package discovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	errEmptyAddress      = errors.New("empty address")
	errNicknameNotFound  = errors.New("nickname not found")
	errEmptyResponseBody = errors.New("empty response")
	errTSDNSNotFound     = errors.New("not found")
)

const (
	TsDnsDefaultPort = "41144"
	NicknameLookup   = "https://named.myteamspeak.com/lookup"
	CacheTTL         = 10 * time.Minute
)

// ResolvedAddr is one resolved host:port and how it was obtained.
type ResolvedAddr struct {
	Expiry time.Time
	Addr   string
	Source string
}

// Resolver resolves TeamSpeak-style addresses (nickname, SRV, TSDNS) with TTL cache.
type Resolver struct {
	log   *slog.Logger
	cache map[string][]ResolvedAddr
	mu    sync.RWMutex
}

// NewResolver returns a Resolver using log for debug tracing (nil → slog.Default).
func NewResolver(log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}

	return &Resolver{
		log:   log,
		cache: make(map[string][]ResolvedAddr),
	}
}

// Resolve tries, in order: MyTeamSpeak nickname, _ts3._udp SRV, TSDNS via SRV and :41144, then plain DNS.
func (r *Resolver) Resolve(ctx context.Context, inputAddr string) ([]ResolvedAddr, error) {
	if inputAddr == "" {
		return nil, errEmptyAddress
	}

	if cached, ok := r.getValidCache(inputAddr); ok {
		return cached, nil
	}

	host, port := splitHostPortOrDefault(inputAddr)

	if ip := net.ParseIP(host); ip != nil {
		return []ResolvedAddr{{Addr: net.JoinHostPort(host, port), Source: "Direct"}}, nil
	}

	if !strings.Contains(host, ".") && host != "localhost" {
		if nickAddr, ok := r.resolveNicknameAddr(ctx, host); ok {
			return r.Resolve(ctx, nickAddr)
		}
	}

	if results, ok := r.resolveSRV(ctx, host); ok {
		return r.setCache(inputAddr, results), nil
	}

	domainList := getDomainList(host)

	if tsdnsAddr, ok := r.resolveTSDNSSRV(ctx, domainList, host); ok {
		results := []ResolvedAddr{{Addr: tsdnsAddr, Source: "TSDNS-SRV"}}

		return r.setCache(inputAddr, results), nil
	}

	if tsdnsAddr, ok := r.resolveTSDNSDirect(ctx, domainList, host); ok {
		results := []ResolvedAddr{{Addr: tsdnsAddr, Source: "TSDNS-Direct"}}

		return r.setCache(inputAddr, results), nil
	}

	r.log.Debug("falling back to direct dns", slog.String("host", host), slog.String("port", port))
	results := []ResolvedAddr{{
		Addr:   net.JoinHostPort(host, port),
		Source: "Direct",
	}}

	return r.setCache(inputAddr, results), nil
}

func (r *Resolver) getValidCache(inputAddr string) ([]ResolvedAddr, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cached, ok := r.cache[inputAddr]
	if !ok || len(cached) == 0 || time.Now().After(cached[0].Expiry) {
		return nil, false
	}
	r.log.Debug("cache hit", slog.String("addr", inputAddr), slog.String("source", cached[0].Source))

	return cached, true
}

func splitHostPortOrDefault(inputAddr string) (string, string) {
	host, port, err := net.SplitHostPort(inputAddr)
	if err != nil {
		return inputAddr, "9987"
	}

	return host, port
}

func (r *Resolver) resolveNicknameAddr(ctx context.Context, host string) (string, bool) {
	r.log.Debug("trying nickname resolution", slog.String("nickname", host))
	nickAddr, err := resolveNickname(ctx, host)
	if err != nil || nickAddr == "" {
		r.log.Debug("nickname resolution failed", slog.String("nickname", host), slog.Any("error", err))

		return "", false
	}
	r.log.Debug("nickname resolved", slog.String("nickname", host), slog.String("result", nickAddr))

	return nickAddr, true
}

func (r *Resolver) resolveSRV(ctx context.Context, host string) ([]ResolvedAddr, bool) {
	r.log.Debug("trying dns srv", slog.String("host", host))
	_, srvs, err := net.DefaultResolver.LookupSRV(ctx, "ts3", "udp", host)
	if err != nil || len(srvs) == 0 {
		r.log.Debug("dns srv failed", slog.String("host", host), slog.Any("error", err))

		return nil, false
	}

	results := make([]ResolvedAddr, 0, len(srvs))
	for _, srv := range srvs {
		target := strings.TrimSuffix(srv.Target, ".")
		results = append(results, ResolvedAddr{
			Addr:   net.JoinHostPort(target, strconv.FormatUint(uint64(srv.Port), 10)),
			Source: "SRV",
		})
	}
	r.log.Debug("dns srv succeeded", slog.String("host", host), slog.String("result", results[0].Addr))

	return results, true
}

func (r *Resolver) resolveTSDNSSRV(ctx context.Context, domains []string, queryHost string) (string, bool) {
	for _, domain := range domains {
		r.log.Debug("trying tsdns srv", slog.String("domain", domain))
		_, srvs, err := net.DefaultResolver.LookupSRV(ctx, "tsdns", "tcp", domain)
		if err != nil || len(srvs) == 0 {
			r.log.Debug("tsdns srv failed", slog.String("domain", domain))

			continue
		}
		for _, srv := range srvs {
			target := strings.TrimSuffix(srv.Target, ".")
			tsdnsAddr, queryErr := queryTSDNS(
				ctx, net.JoinHostPort(target, strconv.FormatUint(uint64(srv.Port), 10)), queryHost,
			)
			if queryErr == nil && tsdnsAddr != "" {
				r.log.Debug("tsdns srv succeeded", slog.String("domain", domain), slog.String("result", tsdnsAddr))

				return tsdnsAddr, true
			}
		}
	}

	return "", false
}

func (r *Resolver) resolveTSDNSDirect(ctx context.Context, domains []string, queryHost string) (string, bool) {
	for _, domain := range domains {
		r.log.Debug("trying tsdns direct", slog.String("domain", domain))
		tsdnsAddr, err := queryTSDNS(ctx, net.JoinHostPort(domain, TsDnsDefaultPort), queryHost)
		if err == nil && tsdnsAddr != "" {
			r.log.Debug("tsdns direct succeeded", slog.String("domain", domain), slog.String("result", tsdnsAddr))

			return tsdnsAddr, true
		}
		r.log.Debug("tsdns direct failed", slog.String("domain", domain))
	}

	return "", false
}

func (r *Resolver) setCache(key string, results []ResolvedAddr) []ResolvedAddr {
	expiry := time.Now().Add(CacheTTL)
	for i := range results {
		results[i].Expiry = expiry
	}
	r.mu.Lock()
	r.cache[key] = results
	r.mu.Unlock()

	return results
}

// getDomainList returns host suffixes ordered longest-first (for TSDNS), capped at three.
func getDomainList(host string) []string {
	parts := strings.Split(host, ".")
	list := make([]string, 0, len(parts)-1)
	for i := range len(parts) - 1 {
		list = append(list, strings.Join(parts[i:], "."))
	}
	if len(list) > 3 {
		return list[:3]
	}

	return list
}

func resolveNickname(ctx context.Context, nickname string) (string, error) {
	lookupURL, err := url.Parse(NicknameLookup)
	if err != nil {
		return "", err
	}
	query := lookupURL.Query()
	query.Set("name", nickname)
	lookupURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL.String(), nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", errNicknameNotFound
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(body), "\n")
	if len(lines) > 0 && lines[0] != "" {
		return strings.TrimSpace(lines[0]), nil
	}

	return "", errEmptyResponseBody
}

func queryTSDNS(ctx context.Context, tsdnsFullAddr, queryHost string) (string, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", tsdnsFullAddr)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	_, err = fmt.Fprintf(conn, "%s\n", queryHost)
	if err != nil {
		return "", err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	line = strings.TrimSpace(line)
	if line == "" || line == "404" || line == "errors" {
		return "", errTSDNSNotFound
	}

	return line, nil
}
