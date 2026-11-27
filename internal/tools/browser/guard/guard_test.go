package guard

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Every name, address and text here is invented.

func TestClickAndTypeAreSideEffectsAndTheRestOnlyRead(t *testing.T) {
	for _, a := range Actions {
		want := a == Click || a == Type
		if SideEffect(a) != want {
			t.Errorf("SideEffect(%s) = %v", a, !want)
		}
	}
	if !SideEffect("submit") || !SideEffect("") {
		t.Error("an action the guard does not know must count as a side effect")
	}
}

func TestTheBlockListMatchesAHostAndTheNamesUnderIt(t *testing.T) {
	list := ParseList(" Bank.Example, *.pay.example\n.login.example. ; ")
	if strings.Join(list, " ") != "bank.example pay.example login.example" {
		t.Fatalf("ParseList read %q", list)
	}
	for host, want := range map[string]bool{
		"bank.example": true, "www.bank.example": true, "BANK.EXAMPLE.": true, "notbank.example": false,
		"pay.example": true, "a.b.pay.example": true, "login.example": true, "harbour.example": false, "": false,
	} {
		if Blocked(host, list) != want {
			t.Errorf("Blocked(%q) = %v", host, !want)
		}
	}
}

func TestAnAddressToOpen(t *testing.T) {
	blocked := []string{"bank.example"}
	for raw, want := range map[string]string{
		"https://harbour.example/notices": "https://harbour.example/notices",
		"harbour.example/notices":         "https://harbour.example/notices",
		"harbour.example:8080/x":          "https://harbour.example:8080/x",
		"HTTP://harbour.example":          "http://harbour.example",
	} {
		u, err := CheckAddress(raw, blocked)
		if err != nil || u.String() != want {
			t.Errorf("CheckAddress(%q) = %v, %v; want %s", raw, u, err, want)
		}
	}
	for _, raw := range []string{"", "javascript:alert(1)", "file:///c:/x", "data:text/html,hi", "ftp://harbour.example",
		"https://keeper:lamp@harbour.example/", "https://www.bank.example/", "https:///nohost", "https://" + strings.Repeat("a", 2100)} {
		if _, err := CheckAddress(raw, blocked); err == nil {
			t.Errorf("CheckAddress(%q) was let through", raw)
		}
	}
}

func TestAPageCannotSendAnythingOnItsOwn(t *testing.T) {
	blocked := []string{"bank.example"}
	ok := []Request{
		{URL: "https://harbour.example/", Method: "GET"},
		{URL: "https://harbour.example/", Method: ""},
		{URL: "https://harbour.example/", Method: "HEAD"},
		{URL: "https://harbour.example/", Method: "OPTIONS"},
		{URL: "https://harbour.example/submit", Method: "POST", Sending: true},
		{URL: "chrome-extension://abc/background.js", Method: "GET"},
	}
	for _, r := range ok {
		if err := CheckRequest(r, blocked); err != nil {
			t.Errorf("CheckRequest(%+v) = %v", r, err)
		}
	}
	for _, r := range []Request{{URL: "https://harbour.example/submit", Method: "POST"}, {URL: "https://harbour.example/x", Method: "PUT"}, {URL: "https://harbour.example/x", Method: "delete"}} {
		if err := CheckRequest(r, blocked); !errors.Is(err, ErrSends) {
			t.Errorf("CheckRequest(%+v) = %v, and a page must not send without an approved click", r, err)
		}
	}
	for _, r := range []Request{
		{URL: "https://bank.example/", Method: "GET"},
		{URL: "https://www.bank.example/pay", Method: "POST", Sending: true},
		{URL: "file:///c:/Users/x", Method: "GET"},
		{URL: "ftp://harbour.example/", Method: "GET"},
		{URL: "ws://harbour.example/", Method: "GET"},
		{URL: "https://keeper:lamp@harbour.example/", Method: "GET"},
		{URL: "chrome-extension://abc/x", Method: "POST"},
	} {
		if err := CheckRequest(r, blocked); err == nil {
			t.Errorf("CheckRequest(%+v) was let through", r)
		}
	}
}

func TestTheBrowserNeverTypesIntoAPasswordPaymentOrSignInField(t *testing.T) {
	refused := map[string]Element{
		"password type":          {Tag: "input", Type: "password", Editable: true},
		"password autocomplete":  {Tag: "input", Type: "text", Autocomplete: "current-password", Editable: true},
		"new password":           {Tag: "input", Type: "text", Autocomplete: "section-a new-password", Editable: true},
		"one-time code":          {Tag: "input", Type: "text", Autocomplete: "one-time-code", Editable: true},
		"card number":            {Tag: "input", Type: "text", Autocomplete: "cc-number", Editable: true},
		"card by name":           {Tag: "input", Type: "tel", Name: "cardNumber", Editable: true},
		"security code label":    {Tag: "input", Type: "text", Label: "Security code", Editable: true},
		"iban":                   {Tag: "input", Type: "text", Placeholder: "IBAN", Editable: true},
		"pin":                    {Tag: "input", Type: "text", Label: "PIN", Editable: true},
		"username":               {Tag: "input", Type: "text", Autocomplete: "username", Editable: true},
		"login name":             {Tag: "input", Type: "email", Name: "login", Editable: true},
		"a sign-in form's field": {Tag: "input", Type: "email", Name: "email", Editable: true, InPasswordForm: true},
		"file":                   {Tag: "input", Type: "file"},
		"not editable":           {Tag: "div"},
	}
	for name, e := range refused {
		if err := CheckType(e, "Saturday at ten"); err == nil {
			t.Errorf("%s: the browser would type into %+v", name, e)
		}
	}
	field := Element{Tag: "input", Type: "text", Name: "visitor", Label: "Your name", Editable: true}
	if err := CheckType(field, "Ada Lantern"); err != nil {
		t.Errorf("a plain text field was refused: %v", err)
	}
	if Kind(field) != "text field" {
		t.Errorf("a plain field is a %s", Kind(field))
	}
	for _, text := range []string{"password: lamp-oil-7", "sk-ant-abcdefabcdef", "Bearer abc123", "", "   "} {
		if err := CheckType(field, text); err == nil {
			t.Errorf("the browser would type %q", text)
		}
	}
}

func TestTheBrowserNeverPressesASignInButtonOrAFileChooser(t *testing.T) {
	blocked := []string{"bank.example"}
	for name, e := range map[string]Element{
		"sign-in button": {Tag: "button", Type: "submit", Label: "Sign in", InPasswordForm: true},
		"sign-in input":  {Tag: "input", Type: "submit", InPasswordForm: true},
		"file chooser":   {Tag: "input", Type: "file"},
		"password field": {Tag: "input", Type: "password", Editable: true},
		"link to a bank": {Tag: "a", Href: "https://www.bank.example/login"},
		"form to a bank": {Tag: "button", Type: "submit", FormAction: "https://bank.example/pay"},
	} {
		if err := CheckClick(e, blocked); err == nil {
			t.Errorf("%s: the browser would click %+v", name, e)
		}
	}
	for name, e := range map[string]Element{
		"plain button": {Tag: "button", Type: "submit", Label: "Send"},
		"plain link":   {Tag: "a", Href: "https://harbour.example/ferries"},
		"checkbox":     {Tag: "input", Type: "checkbox", InPasswordForm: true},
		"script link":  {Tag: "a", Href: "javascript:void(0)"},
	} {
		if err := CheckClick(e, blocked); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

func TestTheProfileHoldsNoCookieButItsOwn(t *testing.T) {
	if err := CheckCookies(nil, nil); err != nil {
		t.Errorf("no cookies failed: %v", err)
	}
	if err := CheckCookies([]Cookie{{Name: "session", Domain: ".mail.example"}}, nil); err == nil {
		t.Error("a cookie in a browser that asked for nothing passed")
	}
	asked := []string{"harbour.example", "www.ferry.example", "127.0.0.1"}
	ok := []Cookie{{Name: "a", Domain: "harbour.example"}, {Name: "b", Domain: ".ferry.example"}, {Name: "c", Domain: "www.ferry.example"}, {Name: "d", Domain: "127.0.0.1"}}
	if err := CheckCookies(ok, asked); err != nil {
		t.Errorf("the browser's own cookies failed: %v", err)
	}
	for _, c := range []Cookie{{Name: "x", Domain: ".mail.example"}, {Name: "y", Domain: "arbour.example"}, {Name: "z", Domain: "sub.harbour.example"}} {
		if err := CheckCookies(append(ok, c), asked); err == nil {
			t.Errorf("a cookie for %s passed", c.Domain)
		}
	}
}

func TestTheProfileIsFreshAndInsideThePersonsFolder(t *testing.T) {
	tenant := filepath.Join(t.TempDir(), "tenants", "0123456789abcdef")
	profile := Profile(tenant)
	if err := CheckProfile(profile, tenant, 0); err != nil {
		t.Errorf("the tenant's own empty profile failed: %v", err)
	}
	if err := CheckProfile(profile, tenant, 3); err == nil {
		t.Error("a profile that is not empty passed")
	}
	for _, other := range []string{filepath.Join(tenant, "files"), filepath.Join(t.TempDir(), "User Data"), filepath.Join(tenant, "..", "fedcba9876543210", ProfileFolder)} {
		if err := CheckProfile(other, tenant, 0); err == nil {
			t.Errorf("the profile %s passed", other)
		}
	}
	if err := CheckProfile(profile, "", 0); err == nil {
		t.Error("a profile with no tenant folder passed")
	}
}

func TestTheBrowserStartsHeadlessOverItsPipeWithItsOwnProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ProfileFolder)
	args := Args(profile, 1280, 800, "--remote-debugging-io-pipes=1,2")
	if err := CheckArgs(args, profile); err != nil {
		t.Fatalf("the guard's own command line failed: %v", err)
	}
	for _, a := range args {
		if strings.Contains(a, "port") || strings.Contains(a, "address") {
			t.Errorf("the command line holds %s", a)
		}
	}
	bad := map[string][]string{
		"a port":          append(Args(profile, 1, 1), "--remote-debugging-port=9222"),
		"an address":      append(Args(profile, 1, 1), "--remote-debugging-address=0.0.0.0"),
		"another profile": append(Args(profile, 1, 1), "--profile-directory=Default"),
		"a second dir":    append(Args(profile, 1, 1), "--user-data-dir="+filepath.Join(t.TempDir(), "User Data")),
		"not headless":    {"--remote-debugging-pipe", "--user-data-dir=" + profile},
		"no pipe":         {"--headless=new", "--user-data-dir=" + profile},
		"no profile":      {"--headless=new", "--remote-debugging-pipe"},
		"a proxy":         append(Args(profile, 1, 1), "--proxy-server=127.0.0.1:9"),
	}
	for name, a := range bad {
		if err := CheckArgs(a, profile); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}
