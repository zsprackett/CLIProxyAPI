package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/httpwire"
)

// captureOAuthRequestHead runs one control-plane call over the production
// header-ordering conn and returns the request head bytes the server read.
func captureOAuthRequestHead(t *testing.T, call func(*ClaudeAuth) error) string {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		if errClose := serverConn.Close(); errClose != nil && !errors.Is(errClose, net.ErrClosed) {
			t.Errorf("close server connection: %v", errClose)
		}
	})
	auth := &ClaudeAuth{httpClient: &http.Client{Transport: &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return httpwire.NewOrderedRequestConn(clientConn, claudeOAuthRequestHeaderOrder), nil
		},
	}}}

	headCh := make(chan string, 1)
	go func() {
		if errDeadline := serverConn.SetDeadline(time.Now().Add(5 * time.Second)); errDeadline != nil {
			headCh <- ""
			return
		}
		reader := bufio.NewReader(serverConn)
		var head bytes.Buffer
		for {
			line, errRead := reader.ReadString('\n')
			head.WriteString(line)
			if errRead != nil || line == "\r\n" {
				break
			}
		}
		headCh <- head.String()
		_, _ = io.WriteString(serverConn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
	}()

	if errCall := call(auth); errCall != nil {
		t.Fatalf("control-plane call: %v", errCall)
	}
	select {
	case head := <-headCh:
		return head
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading the request head")
		return ""
	}
}

func TestFetchOAuthUsageWritesAxiosHeaderOrderWithLowercaseBeta(t *testing.T) {
	head := captureOAuthRequestHead(t, func(auth *ClaudeAuth) error {
		_, errFetch := auth.FetchOAuthUsage(context.Background(), "token-placeholder")
		return errFetch
	})
	want := "GET /api/oauth/usage HTTP/1.1\r\n" +
		"Accept: application/json, text/plain, */*\r\n" +
		"Content-Type: application/json\r\n" +
		"Authorization: Bearer token-placeholder\r\n" +
		"anthropic-beta: oauth-2025-04-20\r\n" +
		"Cache-Control: no-cache\r\n" +
		"User-Agent: axios/1.15.2\r\n" +
		"Accept-Encoding: gzip, compress, deflate, br\r\n" +
		"Host: api.anthropic.com\r\n" +
		"Connection: close\r\n" +
		"\r\n"
	if head != want {
		t.Fatalf("usage request head differs\n got: %q\nwant: %q", head, want)
	}
}

func TestFetchOAuthRolesKeepsAxiosHeaderOrderWithoutBeta(t *testing.T) {
	head := captureOAuthRequestHead(t, func(auth *ClaudeAuth) error {
		_, errFetch := auth.FetchOAuthRoles(context.Background(), "token-placeholder")
		return errFetch
	})
	want := "GET /api/oauth/claude_cli/roles HTTP/1.1\r\n" +
		"Accept: application/json, text/plain, */*\r\n" +
		"Content-Type: application/json\r\n" +
		"Authorization: Bearer token-placeholder\r\n" +
		"Cache-Control: no-cache\r\n" +
		"User-Agent: axios/1.15.2\r\n" +
		"Accept-Encoding: gzip, compress, deflate, br\r\n" +
		"Host: api.anthropic.com\r\n" +
		"Connection: close\r\n" +
		"\r\n"
	if head != want {
		t.Fatalf("roles request head differs\n got: %q\nwant: %q", head, want)
	}
}
