package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type contactInput struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Message   string `json:"message"`
	Website   string `json:"website"`
	Captcha   string `json:"captcha"`
	RequestID string `json:"requestId"`
}

type contactServer struct {
	quotaMu                                        sync.Mutex
	quota                                          quotaState
	key, from, recipient, project, siteKey, origin string
	client                                         *http.Client
	verify                                         func(context.Context, string) error
	reserve                                        func(context.Context, string) error
	send                                           func(context.Context, contactInput, string) error
	tokenMu                                        sync.Mutex
	token                                          string
	tokenExpiry                                    time.Time
	// A bounded per-instance gate also limits calls to billed protection APIs.
	gateMu     sync.Mutex
	gateMinute int64
	gateCount  int
}

var errLimited = errors.New("submission limit reached")
var errCaptcha = errors.New("invalid captcha")
var requestIDPattern = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

func newContactServer() *contactServer {
	s := &contactServer{key: os.Getenv("RESEND_API_KEY"), from: os.Getenv("CONTACT_FROM"), recipient: os.Getenv("CONTACT_TO"), project: os.Getenv("GOOGLE_CLOUD_PROJECT"), siteKey: os.Getenv("RECAPTCHA_SITE_KEY"), origin: os.Getenv("CONTACT_ORIGIN"), client: &http.Client{Timeout: 10 * time.Second}}
	s.verify = s.verifyCaptcha
	s.reserve = s.reserveQuota
	s.send = s.sendEmail
	return s
}

func (s *contactServer) ready() bool {
	return s.key != "" && s.from != "" && s.recipient != "" && s.project != "" && s.siteKey != "" && s.origin != ""
}

func respond(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func (s *contactServer) publicConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		respond(w, 405, "Method not allowed.")
		return
	}
	if !s.ready() {
		respond(w, 503, "The form is temporarily unavailable. Please email me directly.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"siteKey": s.siteKey})
}

func (s *contactServer) submit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		respond(w, 405, "Method not allowed.")
		return
	}
	if !s.ready() {
		respond(w, 503, "The form is temporarily unavailable. Please email me directly.")
		return
	}
	if r.Header.Get("Origin") != s.origin {
		respond(w, 403, "Please send the message from this website.")
		return
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		respond(w, 415, "Please use the website contact form.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 24<<10)
	var input contactInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		respond(w, 400, "Invalid or oversized message.")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		respond(w, 400, "Invalid message.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.TrimSpace(input.Email)
	input.Message = strings.TrimSpace(input.Message)
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email || len(input.Email) > 254 || strings.ContainsAny(input.Email, "\r\n") || !strings.Contains(input.Email, "@") || !utf8.ValidString(input.Name+input.Message) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 120 || strings.ContainsAny(input.Name, "\r\n\x00") || utf8.RuneCountInString(input.Message) < 1 || utf8.RuneCountInString(input.Message) > 5000 || strings.Contains(input.Message, "\x00") || !requestIDPattern.MatchString(input.RequestID) {
		respond(w, 400, "Enter a valid name, email address, and message (up to 5,000 characters).")
		return
	}
	if input.Website != "" {
		respond(w, 400, "Unable to submit this message.")
		return
	}
	if len(input.Captcha) < 1 || len(input.Captcha) > 8192 {
		respond(w, 400, "Please complete the spam check.")
		return
	}
	s.gateMu.Lock()
	minute := time.Now().Unix() / 60
	if minute != s.gateMinute {
		s.gateMinute = minute
		s.gateCount = 0
	}
	s.gateCount++
	allowed := s.gateCount <= 20
	s.gateMu.Unlock()
	if !allowed {
		w.Header().Set("Retry-After", "60")
		respond(w, 429, "Too many attempts. Please wait a minute.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.verify(ctx, input.Captcha); err != nil {
		if errors.Is(err, errCaptcha) {
			respond(w, 400, "Please complete a fresh spam check.")
		} else {
			respond(w, 503, "Spam verification is unavailable. Your message has not been sent.")
		}
		return
	}
	emailHash := s.digest(strings.ToLower(input.Email))
	if err := s.reserve(ctx, emailHash); err != nil {
		if errors.Is(err, errLimited) {
			w.Header().Set("Retry-After", "600")
			respond(w, 429, "The message limit has been reached. Please try later or email me directly.")
		} else {
			respond(w, 503, "Unable to submit right now. Your message has not been sent.")
		}
		return
	}
	// Include the payload in the key, but never expose personal data in the header.
	id := s.digest(input.RequestID + "\x00" + input.Name + "\x00" + input.Email + "\x00" + input.Message)
	if err := s.send(ctx, input, id); err != nil {
		respond(w, 502, "Delivery could not be confirmed. Your text is saved here; retry with the same details or email me directly.")
		return
	}
	respond(w, 200, "Thanks! Your message has been submitted. I’ll get back to you soon.")
}

func (s *contactServer) digest(value string) string {
	h := hmac.New(sha256.New, []byte(s.key))
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *contactServer) jsonRequest(ctx context.Context, method, endpoint, token string, body interface{}, out interface{}) (int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(out)
	}
	return resp.StatusCode, err
}

func (s *contactServer) accessToken(ctx context.Context) (string, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.token != "" && time.Now().Before(s.tokenExpiry) {
		return s.token, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("service identity unavailable")
	}
	var result struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil || result.Token == "" {
		return "", errors.New("service identity unavailable")
	}
	s.token = result.Token
	s.tokenExpiry = time.Now().Add(time.Duration(result.Expires-60) * time.Second)
	return s.token, nil
}

func (s *contactServer) verifyCaptcha(ctx context.Context, captcha string) error {
	token, err := s.accessToken(ctx)
	if err != nil {
		return err
	}
	var result struct {
		Properties struct {
			Valid    bool   `json:"valid"`
			Hostname string `json:"hostname"`
		} `json:"tokenProperties"`
	}
	_, err = s.jsonRequest(ctx, "POST", "https://recaptchaenterprise.googleapis.com/v1/projects/"+s.project+"/assessments", token, map[string]interface{}{"event": map[string]string{"token": captcha, "siteKey": s.siteKey}}, &result)
	if err != nil {
		return err
	}
	origin, err := url.Parse(s.origin)
	if err != nil {
		return err
	}
	if !result.Properties.Valid || result.Properties.Hostname != origin.Hostname() {
		return errCaptcha
	}
	return nil
}

func (s *contactServer) sendEmail(ctx context.Context, input contactInput, id string) error {
	payload := map[string]interface{}{"from": s.from, "to": []string{s.recipient}, "reply_to": input.Email, "subject": "New portfolio contact message", "text": "Name: " + input.Name + "\nEmail: " + input.Email + "\n\n" + input.Message}
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.resend.com/emails", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("email provider status %d", resp.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil {
		return err
	}
	if result.ID == "" {
		return errors.New("missing provider acknowledgement")
	}
	return nil
}
