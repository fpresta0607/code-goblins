package keypages

import (
	"fmt"
	"strings"
	"testing"
)

func TestSignInRecipesNameAReviewedHTTPSPageAndAnchoredPath(t *testing.T) {
	_, err := Parse([]byte(`{"providers":[{"id":"stripe","name":"Stripe","hosts":["stripe.com","*.stripe.com"],"key_page":"https://dashboard.stripe.com/test/apikeys","signed_in_path":"^/(acct_[a-zA-Z0-9]+/)?test/apikeys/?$"}]}`))
	if err != nil {
		t.Fatalf("a reviewed sign-in recipe was refused: %v", err)
	}
}

func TestSignInRecipesRefusePagesOutsideTheirHostsAndUnboundedChecks(t *testing.T) {
	for _, test := range []struct {
		name string
		page string
		path string
	}{
		{"plain HTTP", "http://dashboard.stripe.com/test/apikeys", "^/test/apikeys$"},
		{"another host", "https://elsewhere.example/test/apikeys", "^/test/apikeys$"},
		{"a lookalike host", "https://stripe.com.example/test/apikeys", "^/test/apikeys$"},
		{"credentials in the address", "https://user@dashboard.stripe.com/test/apikeys", "^/test/apikeys$"},
		{"a query", "https://dashboard.stripe.com/test/apikeys?secret=value", "^/test/apikeys$"},
		{"a fragment", "https://dashboard.stripe.com/test/apikeys#secret", "^/test/apikeys$"},
		{"a port", "https://dashboard.stripe.com:8443/test/apikeys", "^/test/apikeys$"},
		{"no page", "", "^/test/apikeys$"},
		{"no check", "https://dashboard.stripe.com/test/apikeys", ""},
		{"an unanchored check", "https://dashboard.stripe.com/test/apikeys", "/test/apikeys"},
		{"an invalid check", "https://dashboard.stripe.com/test/apikeys", "^/[$"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`{"providers":[{"id":"stripe","name":"Stripe","hosts":["stripe.com","*.stripe.com"],"key_page":%q,"signed_in_path":%q}]}`, test.page, test.path)
			_, err := Parse([]byte(data))
			if err == nil || !strings.Contains(err.Error(), "sign-in") {
				t.Fatalf("Parse = %v, want a refusal naming the sign-in recipe", err)
			}
		})
	}
}
