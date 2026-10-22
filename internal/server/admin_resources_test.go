package server

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminChannelAPIUsesCASAndDefaultGuards(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	a, cookie, csrf := testAdminLogin(t, s, owner.identity)
	_, rev := s.config.Store.state()
	body := fmt.Sprintf(`{"revision":%d,"name":"Web channel","description":"test","bitrate":32000}`, rev)
	if w := adminRequest(a, "POST", "/channels", body, csrf, cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := adminRequest(a, "POST", "/channels", body, csrf, cookie); w.Code != 409 {
		t.Fatal("stale form accepted", w.Code)
	}
	_, rev = s.config.Store.state()
	if w := adminRequest(a, "DELETE", "/channels/1", fmt.Sprintf(`{"revision":%d}`, rev), csrf, cookie); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := adminRequest(a, "POST", "/channels/3/order", fmt.Sprintf(`{"revision":%d,"before_id":1}`, rev), csrf, cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	state := s.adminSnapshot(owner.identity)
	if state.Channels[0].ID != 3 || state.DefaultChannel != 1 {
		t.Fatal("order/default changed incorrectly")
	}
	for _, c := range state.Credentials {
		if c.Digest != "" {
			t.Fatal("credential digest exposed")
		}
	}
}

func TestAdminEventsDeliverChangesAndExitOnLogout(t *testing.T) {
	s, owner, _ := channelTestServer(t)
	a, cookie, csrf := testAdminLogin(t, s, owner.identity)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "127.0.0.1:9989"; a.ServeHTTP(w, r) }))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+adminBase+"/events", nil)
	req.AddCookie(cookie)
	response, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	readEvent := func() string {
		t.Helper()
		var data strings.Builder
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			data.WriteString(line)
			if line == "\n" {
				return data.String()
			}
		}
	}
	if event := readEvent(); !strings.Contains(event, "event: state") {
		t.Fatal(event)
	}
	_, rev := s.config.Store.state()
	body := fmt.Sprintf(`{"revision":%d,"name":"SSE channel","bitrate":32000}`, rev)
	if w := adminRequest(a, "POST", "/channels", body, csrf, cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if event := readEvent(); !strings.Contains(event, "SSE channel") {
		t.Fatal("missing update", event)
	}
	if w := adminRequest(a, "POST", "/logout", `{}`, csrf, cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if event := readEvent(); !strings.Contains(event, "event: expired") {
		t.Fatal(event)
	}
	if _, err = reader.ReadByte(); err == nil {
		t.Fatal("SSE remained open after logout")
	}
}
