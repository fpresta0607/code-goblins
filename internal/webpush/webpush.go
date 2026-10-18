// Package webpush sends a Web Push message (RFC 8030) to a browser's push
// subscription, encrypted so only that browser reads it (RFC 8291) and
// authorized by the application server's VAPID keys (RFC 8292).
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// recordSize is the one record a push is: its payload, the delimiter that
// ends it and the AES-GCM tag fit in it.
const recordSize = 4096

// maxContent is the most a push carries, with its padding: what a body of
// 4096 bytes, the most a push service must take, holds after the header and
// its key, the AES-GCM tag and the delimiter (RFC 8291 section 4).
const maxContent = 4096 - 86 - 16 - 1

// paddedSize is the size Send pads every payload to, so a push service sees
// no push's length.
const paddedSize = 2048

// Keys is the application server's VAPID key pair, which signs each push so
// the push service delivers only what the server the browser subscribed to
// sends.
type Keys struct {
	private *ecdsa.PrivateKey
}

// GenerateKeys makes a new key pair.
func GenerateKeys() (Keys, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return Keys{private}, err
}

// ParseKeys reads a key pair Marshal kept.
func ParseKeys(der []byte) (Keys, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return Keys{}, err
	}
	private, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || private.Curve != elliptic.P256() {
		return Keys{}, errors.New("VAPID keys are a P-256 key pair")
	}
	return Keys{private}, nil
}

// Marshal is the key pair as PKCS #8, to keep.
func (k Keys) Marshal() ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(k.private)
}

// Public is the public key a browser subscribes with, its
// applicationServerKey: the uncompressed P-256 point, base64url.
func (k Keys) Public() string {
	point, err := k.private.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(point)
}

// authorization is the VAPID Authorization header for a push to the push
// service at audience, naming contact as who runs the server, until expires.
func (k Keys) authorization(audience, contact string, expires time.Time) (string, error) {
	claims, err := json.Marshal(struct {
		Audience string `json:"aud"`
		Expires  int64  `json:"exp"`
		Contact  string `json:"sub"`
	}{audience, expires.Unix(), contact})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return "vapid t=" + unsigned + "." + base64.RawURLEncoding.EncodeToString(signature) + ", k=" + k.Public(), nil
}

// encrypt is payload, with padding zero bytes after it, as one aes128gcm
// record for the browser whose key and auth secret its subscription gives,
// from server's one-time key and salt.
func encrypt(payload []byte, padding int, browserKey, authSecret []byte, server *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload)+padding > maxContent {
		return nil, fmt.Errorf("a push holds at most %d bytes", maxContent)
	}
	if len(authSecret) != 16 || len(salt) != 16 {
		return nil, errors.New("a subscription's auth secret and a push's salt are 16 bytes")
	}
	browser, err := ecdh.P256().NewPublicKey(browserKey)
	if err != nil {
		return nil, fmt.Errorf("a subscription's key: %w", err)
	}
	shared, err := server.ECDH(browser)
	if err != nil {
		return nil, err
	}
	serverKey := server.PublicKey().Bytes()
	keyInfo := slices.Concat([]byte("WebPush: info\x00"), browserKey, serverKey)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	header := binary.BigEndian.AppendUint32(slices.Clone(salt), recordSize)
	header = append(append(header, byte(len(serverKey))), serverKey...)
	plaintext := append(append(slices.Clone(payload), 2), make([]byte, padding)...)
	return gcm.Seal(header, nonce, plaintext, nil), nil
}

// Subscription is a browser's push subscription as PushSubscription.toJSON
// gives it: where its push service takes pushes for it, and its P-256 key and
// auth secret, base64url.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// ErrGone is a subscription its push service no longer has, which is
// dropped; errFailed a push the service refused otherwise, or answered with a
// redirect, which Send never follows.
var (
	ErrGone   = errors.New("the push service no longer has this subscription")
	errFailed = errors.New("the push service refused the push")
)

// Send encrypts payload for sub, padded to paddedSize, and posts it to its
// push service, which holds it for at most ttl while the browser is away.
// contact is who runs the server, which the authorization names. Its errors
// name the push service's host, never the subscription's address.
func Send(ctx context.Context, client *http.Client, keys Keys, contact string, sub Subscription, payload []byte, ttl time.Duration) error {
	browserKey, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return fmt.Errorf("a subscription's key: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		return fmt.Errorf("a subscription's auth secret: %w", err)
	}
	server, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encrypt(payload, max(0, paddedSize-len(payload)), browserKey, authSecret, server, salt)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(sub.Endpoint)
	if err != nil {
		return err
	}
	authorization, err := keys.authorization(endpoint.Scheme+"://"+endpoint.Host, contact, time.Now().Add(12*time.Hour))
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	once := *client
	once.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := once.Do(request)
	if err != nil {
		return fmt.Errorf("the push service at %s could not be reached", endpoint.Host)
	}
	defer response.Body.Close()
	// A refusal says why in a word or two, as Apple's {"reason": ...} does.
	var refusal struct {
		Reason string `json:"reason"`
	}
	answer, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	_ = json.Unmarshal(answer, &refusal)
	switch {
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return ErrGone
	case response.StatusCode/100 != 2 && refusal.Reason != "" && len(refusal.Reason) <= 64:
		return fmt.Errorf("%w: %s answered %s (%s)", errFailed, endpoint.Host, response.Status, refusal.Reason)
	case response.StatusCode/100 != 2:
		return fmt.Errorf("%w: %s answered %s", errFailed, endpoint.Host, response.Status)
	}
	return nil
}

// Endpoint reports whether endpoint is a push service a browser subscribes
// through, Apple's, Google's, Mozilla's or Microsoft's, at its https origin,
// so a push goes nowhere else.
func Endpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	// A host of other letters is refused, as one that only folds to a push
	// service's name: the client would connect to its punycode instead.
	isPlain := func(r rune) bool {
		return r < 128 && (r == '.' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || strings.ContainsFunc(u.Hostname(), func(r rune) bool { return !isPlain(r) }) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "web.push.apple.com" || host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || strings.HasSuffix(host, ".notify.windows.com")
}
