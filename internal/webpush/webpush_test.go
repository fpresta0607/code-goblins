package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decoded(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// testBrowser is a browser's push keys, made for the run, so a test reads
// what was pushed to it.
type testBrowser struct {
	private *ecdh.PrivateKey
	auth    []byte
}

func newBrowser(t *testing.T) testBrowser {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return testBrowser{private, auth}
}

// subscription is the browser's subscription at endpoint.
func (b testBrowser) subscription(endpoint string) Subscription {
	return Subscription{Endpoint: endpoint, P256dh: base64.RawURLEncoding.EncodeToString(b.private.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(b.auth)}
}

// read is a push's body as the browser reads it by RFC 8291 section 3.4,
// written apart from encrypt, with HKDF made of HMAC as RFC 5869 says: the
// salt, record size and server's one-time key from its header, the key and
// nonce from those, and its payload up to the delimiter, with the padding
// after it.
func (b testBrowser) read(t *testing.T, body []byte) ([]byte, int) {
	t.Helper()
	if len(body) < 21+65+16 || int(body[20]) != 65 || binary.BigEndian.Uint32(body[16:20]) != 4096 {
		t.Fatalf("a push of %d bytes has no RFC 8291 header with a 4096-byte record and a 65-byte key", len(body))
	}
	salt, serverKey, record := body[:16], body[21:86], body[86:]
	server, err := ecdh.P256().NewPublicKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := b.private.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	extract := func(salt, input []byte) []byte {
		mac := hmac.New(sha256.New, salt)
		mac.Write(input)
		return mac.Sum(nil)
	}
	expand := func(key []byte, info string, size int) []byte {
		mac := hmac.New(sha256.New, key)
		mac.Write(append([]byte(info), 1))
		return mac.Sum(nil)[:size]
	}
	input := expand(extract(b.auth, shared), "WebPush: info\x00"+string(b.private.PublicKey().Bytes())+string(serverKey), 32)
	key := extract(salt, input)
	block, err := aes.NewCipher(expand(key, "Content-Encoding: aes128gcm\x00", 16))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, expand(key, "Content-Encoding: nonce\x00", 12), record, nil)
	if err != nil {
		t.Fatalf("the push cannot be read with the browser's keys: %v", err)
	}
	end := len(bytes.TrimRight(plain, "\x00")) - 1
	if end < 0 || plain[end] != 2 {
		t.Fatalf("the push's record ends with no last-record delimiter")
	}
	return plain[:end], len(plain) - end - 1
}

// A push is encrypted for the browser that subscribed as RFC 8291 says, so
// only that browser reads it, with its padding after the delimiter, and its
// header names the salt and the server's one-time key.
func TestAPushIsEncryptedAsRFC8291Says(t *testing.T) {
	// Arrange
	browser := newBrowser(t)
	server, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	payload := []byte("When I grow up, I want to be a watermelon")

	// Act
	body, err := encrypt(payload, 10, browser.private.PublicKey().Bytes(), browser.auth, server, salt)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	read, padding := browser.read(t, body)
	if !bytes.Equal(read, payload) || padding != 10 || !bytes.Equal(body[:16], salt) || !bytes.Equal(body[21:86], server.PublicKey().Bytes()) || len(body) != 86+len(payload)+1+10+16 {
		t.Errorf("the browser read %q with %d bytes of padding from a %d-byte push, want %q with 10", read, padding, len(body), payload)
	}
}

// What cannot be a browser's subscription keys, or more than a push service
// must take, 3993 bytes with padding (RFC 8291 section 4), is refused.
func TestEncryptionRefusesWhatNoBrowserSent(t *testing.T) {
	browser := newBrowser(t)
	server, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	salt := make([]byte, 16)
	for name, c := range map[string]struct {
		payload       []byte
		padding       int
		browser, auth []byte
		isTaken       bool
	}{
		"a key that is no point":             {[]byte("hi"), 0, []byte("not a key"), browser.auth, false},
		"a short secret":                     {[]byte("hi"), 0, browser.private.PublicKey().Bytes(), []byte("short"), false},
		"all a push service must take":       {make([]byte, 3000), 993, browser.private.PublicKey().Bytes(), browser.auth, true},
		"more than a push service must take": {make([]byte, 3000), 994, browser.private.PublicKey().Bytes(), browser.auth, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			body, err := encrypt(c.payload, c.padding, c.browser, c.auth, server, salt)

			// Assert
			if (err == nil) != c.isTaken || c.isTaken && len(body) != 4096 {
				t.Errorf("encrypt = %d bytes, %v; want it taken: %v", len(body), err, c.isTaken)
			}
		})
	}
}

// signedBy reports whether a VAPID JWT is signed by keys and returns its
// claims.
func signedBy(t *testing.T, keys Keys, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT %q is not three parts", jwt)
	}
	var header map[string]string
	if err := json.Unmarshal(decoded(t, parts[0]), &header); err != nil || header["typ"] != "JWT" || header["alg"] != "ES256" {
		t.Fatalf("JWT header %v, %v; want typ JWT, alg ES256", header, err)
	}
	signature := decoded(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(signature) != 64 || !ecdsa.Verify(&keys.private.PublicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatalf("JWT signature does not verify with the server's key")
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// Each push carries a VAPID authorization (RFC 8292) for the push service it
// goes to, signed by the server's keys, which name themselves in it, so the
// service delivers only what the server the browser subscribed to sends.
func TestAPushIsAuthorizedByTheServersKeys(t *testing.T) {
	// Arrange
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Unix(1_800_000_000, 0)

	// Act
	header, err := keys.authorization("https://web.push.apple.com", "https://example.com/contact", expires)

	// Assert
	token, key, found := strings.Cut(strings.TrimPrefix(header, "vapid t="), ", k=")
	if err != nil || !strings.HasPrefix(header, "vapid t=") || !found || key != keys.Public() {
		t.Fatalf("authorization = %q, %v; want vapid t=<JWT>, k=%s", header, err, keys.Public())
	}
	claims := signedBy(t, keys, token)
	if claims["aud"] != "https://web.push.apple.com" || claims["sub"] != "https://example.com/contact" || claims["exp"] != float64(1_800_000_000) {
		t.Errorf("claims = %v, want the push service, the contact and the expiry", claims)
	}
}

// The server's keys are kept as they were made, and the public key is the
// one a browser subscribes with: an uncompressed P-256 point.
func TestTheServersKeysAreKeptAndNamedAsABrowserTakesThem(t *testing.T) {
	// Arrange
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	kept, err := keys.Marshal()
	again, againErr := ParseKeys(kept)

	// Assert
	if err != nil || againErr != nil || again.Public() != keys.Public() {
		t.Fatalf("kept keys read back as %q, %v, %v; want %q", again.Public(), err, againErr, keys.Public())
	}
	if point := decoded(t, keys.Public()); len(point) != 65 || point[0] != 4 {
		t.Errorf("public key is %d bytes starting %x, want an uncompressed P-256 point", len(point), point[:1])
	}
	if _, err := ParseKeys([]byte("not a key")); err == nil {
		t.Error("ParseKeys took what is no key")
	}
}

// A push goes to the subscription's endpoint as RFC 8030 asks, encrypted,
// padded to one size and authorized, with how long the service may hold it;
// a subscription the service no longer has is told apart, so it can be
// dropped; a redirect is never followed; and no error names the
// subscription's address.
func TestSendDeliversAndTellsAGoneSubscription(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		answer string
		want   error
		says   string
	}{
		"delivered":     {http.StatusCreated, "", nil, ""},
		"unsubscribed":  {http.StatusGone, "", ErrGone, ""},
		"never known":   {http.StatusNotFound, "", ErrGone, ""},
		"service fault": {http.StatusForbidden, `{"reason":"BadJwtToken"}`, errFailed, "403 Forbidden (BadJwtToken)"},
		"a redirect":    {http.StatusTemporaryRedirect, "", errFailed, "307 Temporary Redirect"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			keys, err := GenerateKeys()
			if err != nil {
				t.Fatal(err)
			}
			var got *http.Request
			var body []byte
			requests := 0
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				got = r
				body, _ = io.ReadAll(r.Body)
				if c.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.answer))
			}))
			defer service.Close()

			// Act
			browser := newBrowser(t)
			err = Send(context.Background(), service.Client(), keys, "https://example.com/contact", browser.subscription(service.URL+"/push/abc"), []byte(`{"count":2}`), time.Hour)

			// Assert
			if !errors.Is(err, c.want) || c.want == nil && err != nil || err != nil && (!strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), "/push/abc")) {
				t.Fatalf("Send = %v, want %v saying %q and not the subscription's address", err, c.want, c.says)
			}
			token, _, _ := strings.Cut(strings.TrimPrefix(got.Header.Get("Authorization"), "vapid t="), ", k=")
			if requests != 1 || got.Method != "POST" || got.URL.Path != "/push/abc" || got.Header.Get("Content-Encoding") != "aes128gcm" || got.Header.Get("TTL") != "3600" || len(body) != 86+paddedSize+1+16 {
				t.Errorf("%d requests, the last %s %s with %v and %d bytes, want one encrypted POST of %d bytes with a TTL of an hour", requests, got.Method, got.URL, got.Header, len(body), 86+paddedSize+1+16)
			}
			if read, padding := browser.read(t, body); string(read) != `{"count":2}` || padding != paddedSize-len(read) {
				t.Errorf("the browser read %q with %d bytes of padding, want the payload padded to %d", read, padding, paddedSize)
			}
			if claims := signedBy(t, keys, token); claims["aud"] != service.URL {
				t.Errorf("audience %v, want the push service's origin %s", claims["aud"], service.URL)
			}
		})
	}
}

// Only a push service a browser subscribes through is sent to: an https
// endpoint of Apple's, Google's, Mozilla's or Microsoft's.
func TestOnlyABrowsersPushServiceIsAnEndpoint(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"https://web.push.apple.com/QGuQyavXutnMH":              true,
		"https://fcm.googleapis.com/fcm/send/abc":               true,
		"https://updates.push.services.mozilla.com/wpush/v2/ab": true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":   true,
		"http://web.push.apple.com/QGuQyavXutnMH":               false,
		"https://web.push.apple.com.example.com/x":              false,
		"https://127.0.0.1:4310/api/remote":                     false,
		"https://user@web.push.apple.com/x":                     false,
		"https://web.push.apple.com:8443/x":                     false,
		"https://WEB.PUSH.APPLE.COM/x":                          true,
		// A dotted capital I, which lowercases to the i of mozilla.
		"https://updates.push.services.mozİlla.com/x": false,
		"https://fcm.googleap%C4%B0s.com/x":           false,
		"not a url":                                   false,
	} {
		if got := Endpoint(endpoint); got != want {
			t.Errorf("Endpoint(%q) = %v, want %v", endpoint, got, want)
		}
	}
}
