package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
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

// RFC 8291 Appendix A: a push its encryption must reproduce exactly.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

// A push is encrypted for the browser that subscribed as RFC 8291 says, so
// only that browser can read it: the example in its appendix comes out byte
// for byte.
func TestAPushIsEncryptedAsRFC8291Says(t *testing.T) {
	// Arrange
	server, err := ecdh.P256().NewPrivateKey(decoded(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	body, err := encrypt([]byte(rfcPlaintext), 0, decoded(t, rfcUAPublic), decoded(t, rfcAuth), server, decoded(t, rfcSalt))

	// Assert
	if err != nil || base64.RawURLEncoding.EncodeToString(body) != rfcBody {
		t.Errorf("encrypt = %s, %v; want %s", base64.RawURLEncoding.EncodeToString(body), err, rfcBody)
	}
}

// What cannot be a browser's subscription keys, or more than a push service
// must take, 3993 bytes with padding (RFC 8291 section 4), is refused.
func TestEncryptionRefusesWhatNoBrowserSent(t *testing.T) {
	server, err := ecdh.P256().NewPrivateKey(decoded(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		payload       []byte
		padding       int
		browser, auth []byte
		isTaken       bool
	}{
		"a key that is no point":             {[]byte("hi"), 0, []byte("not a key"), decoded(t, rfcAuth), false},
		"a short secret":                     {[]byte("hi"), 0, decoded(t, rfcUAPublic), []byte("short"), false},
		"all a push service must take":       {make([]byte, 3000), 993, decoded(t, rfcUAPublic), decoded(t, rfcAuth), true},
		"more than a push service must take": {make([]byte, 3000), 994, decoded(t, rfcUAPublic), decoded(t, rfcAuth), false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			body, err := encrypt(c.payload, c.padding, c.browser, c.auth, server, decoded(t, rfcSalt))

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

// browser is a subscription made with the RFC example's browser keys.
func browser(endpoint string) Subscription {
	return Subscription{Endpoint: endpoint, P256dh: rfcUAPublic, Auth: rfcAuth}
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
			err = Send(context.Background(), service.Client(), keys, "https://example.com/contact", browser(service.URL+"/push/abc"), []byte(`{"count":2}`), time.Hour)

			// Assert
			if !errors.Is(err, c.want) || c.want == nil && err != nil || err != nil && (!strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), "/push/abc")) {
				t.Fatalf("Send = %v, want %v saying %q and not the subscription's address", err, c.want, c.says)
			}
			token, _, _ := strings.Cut(strings.TrimPrefix(got.Header.Get("Authorization"), "vapid t="), ", k=")
			if requests != 1 || got.Method != "POST" || got.URL.Path != "/push/abc" || got.Header.Get("Content-Encoding") != "aes128gcm" || got.Header.Get("TTL") != "3600" || len(body) != 86+paddedSize+1+16 {
				t.Errorf("%d requests, the last %s %s with %v and %d bytes, want one encrypted POST of %d bytes with a TTL of an hour", requests, got.Method, got.URL, got.Header, len(body), 86+paddedSize+1+16)
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
