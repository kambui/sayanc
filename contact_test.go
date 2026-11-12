package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer() *contactServer {
	s := &contactServer{key: "test-key", from: "Website <contact@example.com>", recipient: "owner@example.com", project: "project", siteKey: "public-key", origin: "https://dev.example.com"}
	s.verify = func(context.Context, string) error { return nil }
	s.reserve = func(context.Context, string) error { return nil }
	s.send = func(context.Context, contactInput, string) error { return nil }
	return s
}
func validInput() contactInput {
	return contactInput{Name: "Visitor", Email: "visitor@example.com", Message: "Hello there", Captcha: "test-captcha", RequestID: "550e8400-e29b-41d4-a716-446655440000"}
}
func submitTest(s *contactServer, in contactInput, origin string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", "/api/contact", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	s.submit(w, r)
	return w
}
func TestSubmitValidationAndFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*contactServer, *contactInput)
		status int
	}{
		{"success", func(s *contactServer, i *contactInput) {}, 200},
		{"missing config", func(s *contactServer, i *contactInput) { s.key = "" }, 503},
		{"header injection", func(s *contactServer, i *contactInput) { i.Email = "x@example.com\r\nBcc: other@example.com" }, 400},
		{"empty message", func(s *contactServer, i *contactInput) { i.Message = "  " }, 400},
		{"long message", func(s *contactServer, i *contactInput) { i.Message = strings.Repeat("x", 5001) }, 400},
		{"honeypot", func(s *contactServer, i *contactInput) { i.Website = "spam" }, 400},
		{"missing captcha", func(s *contactServer, i *contactInput) { i.Captcha = "" }, 400},
		{"invalid captcha", func(s *contactServer, i *contactInput) {
			s.verify = func(context.Context, string) error { return errCaptcha }
		}, 400},
		{"captcha outage", func(s *contactServer, i *contactInput) {
			s.verify = func(context.Context, string) error { return errors.New("offline") }
		}, 503},
		{"quota", func(s *contactServer, i *contactInput) {
			s.reserve = func(context.Context, string) error { return errLimited }
		}, 429},
		{"limiter failure", func(s *contactServer, i *contactInput) {
			s.reserve = func(context.Context, string) error { return errors.New("offline") }
		}, 503},
		{"resend outage", func(s *contactServer, i *contactInput) {
			s.send = func(context.Context, contactInput, string) error { return errors.New("offline") }
		}, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			in := validInput()
			sent := false
			s.send = func(context.Context, contactInput, string) error { sent = true; return nil }
			tc.mutate(s, &in)
			w := submitTest(s, in, s.origin)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.status != 200 && sent {
				t.Fatal("invalid submission sent")
			}
		})
	}
}
func TestOriginAndPublicConfig(t *testing.T) {
	s := testServer()
	if w := submitTest(s, validInput(), "https://evil.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := httptest.NewRecorder()
	s.publicConfig(w, httptest.NewRequest("GET", "/api/contact/config", nil))
	if strings.Contains(w.Body.String(), s.key) || strings.Contains(w.Body.String(), s.recipient) {
		t.Fatal("private configuration exposed")
	}
}
func TestQuota(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	q := quotaState{}
	if q.take(now, "a") != nil || q.take(now, "a") != errLimited {
		t.Fatal("sender cooldown")
	}
	if q.take(now, "b") != nil || q.take(now, "c") != nil || q.take(now, "d") != errLimited {
		t.Fatal("global minute limit")
	}
	if q.take(now.Add(10*time.Minute), "a") != nil {
		t.Fatal("cooldown expiry")
	}
	q.Count = 50
	if q.take(now.Add(time.Hour), "new") != errLimited {
		t.Fatal("daily ceiling")
	}
	if q.take(now.Add(24*time.Hour), "new") != nil || q.Count != 1 {
		t.Fatal("day rollover")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResendPayloadAndIdempotency(t *testing.T) {
	s := testServer()
	in := validInput()
	var keys []string
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("wrong provider request")
		}
		var data struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			ReplyTo string   `json:"reply_to"`
			Text    string   `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&data)
		if data.From != s.from || len(data.To) != 1 || data.To[0] != s.recipient || data.ReplyTo != in.Email || !strings.Contains(data.Text, in.Message) {
			t.Fatal("wrong email fields")
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"email-id"}`))}, nil
	})}
	s.send = s.sendEmail
	for n := 0; n < 2; n++ {
		if w := submitTest(s, in, s.origin); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] || len(keys[0]) != 64 {
		t.Fatal("unstable idempotency key")
	}
}
