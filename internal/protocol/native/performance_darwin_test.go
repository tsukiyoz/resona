package native

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

// Counting relays preserve UDP datagrams, including coalesced QUIC packets.
// Their CPU and allocations are included in the whole-process measurements.
type perfRelay struct {
	front, back    *net.UDPConn
	bytes, packets atomic.Int64
	wg             sync.WaitGroup
}

func newPerfRelay(t *testing.T, address string) *perfRelay {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		t.Fatal(err)
	}
	front, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	back, err := net.DialUDP("udp4", nil, remote)
	if err != nil {
		front.Close()
		t.Fatal(err)
	}
	p := &perfRelay{front: front, back: back}
	var peer atomic.Pointer[net.UDPAddr]
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, a, e := front.ReadFromUDP(buf)
			if e != nil {
				return
			}
			peer.Store(a)
			p.bytes.Add(int64(n))
			p.packets.Add(1)
			if _, e = back.Write(buf[:n]); e != nil {
				return
			}
		}
	}()
	go func() {
		defer p.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, e := back.Read(buf)
			if e != nil {
				return
			}
			p.bytes.Add(int64(n))
			p.packets.Add(1)
			if a := peer.Load(); a != nil {
				if _, e = front.WriteToUDP(buf[:n], a); e != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { front.Close(); back.Close(); p.wg.Wait() })
	return p
}

func perfCPU(t *testing.T) float64 {
	t.Helper()
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		t.Fatal(err)
	}
	return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
}

// Opt-in local protocol comparison. Payload is an 80-byte opaque voice surrogate,
// not encoded audio; no microphone, codec, GUI or remote server is involved.
func TestTransportPerformance(t *testing.T) {
	if os.Getenv("RESONA_TRANSPORT_PERF") != "1" {
		t.Skip("set RESONA_TRANSPORT_PERF=1; run without -race")
	}
	for round := 0; round < 3; round++ {
		for _, scenario := range []struct {
			name              string
			members, speakers int
		}{{"idle4", 4, 0}, {"voice4", 4, 1}, {"voice16", 16, 4}} {
			for order := 0; order < 2; order++ {
				noise := (order+round)%2 == 1
				mode := "quic"
				if noise {
					mode = "noise"
				}
				t.Run(fmt.Sprintf("%d/%s/%s", round+1, scenario.name, mode), func(t *testing.T) {
					p, stop, done := startServer(t, noise)
					t.Cleanup(func() { stop(); <-done })
					address := p.Address
					var clients []*connection
					var relays []*perfRelay
					var connects []float64
					var mu sync.Mutex
					latencies := make([]float64, 0, 40000)
					origin := time.Now()
					for i := 0; i < scenario.members; i++ {
						r := newPerfRelay(t, address)
						relays = append(relays, r)
						p.Address = r.front.LocalAddr().String()
						p.Nickname = fmt.Sprintf("perf-%d", i)
						at := time.Now()
						c, _ := connectTest(t, p)
						connects = append(connects, float64(time.Since(at).Microseconds())/1000)
						clients = append(clients, c)
						c.SetVoiceHandler(func(p audio.Packet) {
							if len(p.Data) != 80 {
								return
							}
							d := time.Since(origin) - time.Duration(binary.BigEndian.Uint64(p.Data[:8]))
							mu.Lock()
							latencies = append(latencies, float64(d)/float64(time.Millisecond))
							mu.Unlock()
						})
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						err := c.SetVoiceMuted(ctx, i >= scenario.speakers, false)
						cancel()
						if err != nil {
							t.Fatal(err)
						}
						// Stay below the experimental Noise handshake admission rate.
						time.Sleep(110 * time.Millisecond)
					}
					time.Sleep(500 * time.Millisecond)
					totals := func() (int64, int64) {
						var b, n int64
						for _, r := range relays {
							b += r.bytes.Load()
							n += r.packets.Load()
						}
						return b, n
					}
					handshakeBytes, _ := totals()
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					b0, n0 := totals()
					cpu0 := perfCPU(t)
					start := time.Now()
					attempted := 0
					if scenario.speakers == 0 {
						time.Sleep(30 * time.Second)
					} else {
						ticker := time.NewTicker(20 * time.Millisecond)
						for frame := 0; frame < 500; frame++ {
							<-ticker.C
							for i := 0; i < scenario.speakers; i++ {
								data := make([]byte, 80)
								binary.BigEndian.PutUint64(data[:8], uint64(time.Since(origin)))
								if err := clients[i].SendVoice(data, audio.CodecOpusVoice); err != nil {
									t.Fatal(err)
								}
								attempted++
							}
						}
						ticker.Stop()
					}
					time.Sleep(100 * time.Millisecond)
					elapsed := time.Since(start).Seconds()
					cpu := perfCPU(t) - cpu0
					b1, n1 := totals()
					runtime.ReadMemStats(&after)
					mu.Lock()
					samples := append([]float64(nil), latencies...)
					mu.Unlock()
					sort.Float64s(samples)
					sort.Float64s(connects)
					percentile := func(v []float64, q float64) float64 {
						if len(v) == 0 {
							return 0
						}
						return v[int(float64(len(v)-1)*q)]
					}
					row := map[string]any{
						"round": round + 1, "mode": mode, "scenario": scenario.name,
						"seconds": elapsed, "cpu_percent_one_core": cpu / elapsed * 100,
						"heap_mib":             float64(after.HeapAlloc) / (1 << 20),
						"alloc_mib_per_second": float64(after.TotalAlloc-before.TotalAlloc) / (1 << 20) / elapsed,
						"goroutines":           runtime.NumGoroutine(),
						"udp_bytes":            b1 - b0, "udp_packets": n1 - n0,
						"ipv4_kib_per_second": float64(b1-b0+28*(n1-n0)) / 1024 / elapsed,
						"sent_voice":          attempted, "expected_deliveries": attempted * (scenario.members - 1),
						"received_voice": len(samples),
						"latency_p50_ms": percentile(samples, .5), "latency_p95_ms": percentile(samples, .95),
						"latency_p99_ms": percentile(samples, .99),
						"connect_p50_ms": percentile(connects, .5), "setup_udp_bytes": handshakeBytes,
					}
					line, _ := json.Marshal(row)
					fmt.Printf("PERF %s\n", line)
				})
			}
		}
	}
}
