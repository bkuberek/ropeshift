package api_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

const (
	fingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	secret      = "SECRETJOINTOKEN-9d2c"
)

// join makes a join string by hand, as another version of the program would, so a test reads what the format is and
// not what the encoder happens to write.
func join(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return "ropeshift-join-1." + base64.RawURLEncoding.EncodeToString(raw)
}

// A join string is a format version, then the unpadded base64url of one JSON object with the URL workers reach the
// server at, the fingerprint of its certificate authority when it has one, and the join token.
func TestAJoinStringIsAVersionAndOneBase64URLObject(t *testing.T) {
	t.Parallel()
	for _, want := range []api.JoinString{
		{WorkerURL: "https://ropeshift.lan:7400", CASHA256: fingerprint, Token: secret},
		{WorkerURL: "https://ropeshift.example:443", Token: secret},
		{WorkerURL: "http://127.0.0.1:7400", Token: secret},
		{WorkerURL: "https://[2001:db8::7]:7400", CASHA256: fingerprint, Token: secret},
		{WorkerURL: "https://ropeshift.lan:7400", Token: secret},
	} {
		t.Run(want.WorkerURL, func(t *testing.T) {
			text, err := want.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(text, api.JoinPrefix) || api.JoinPrefix != "ropeshift-join-1." {
				t.Fatalf("the join string begins %q", text[:min(len(text), 20)])
			}
			body := strings.TrimPrefix(text, api.JoinPrefix)
			if strings.ContainsAny(body, "=+/ \n") {
				t.Errorf("the body is not unpadded base64url: %q", body)
			}
			raw, err := base64.RawURLEncoding.DecodeString(body)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]string
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["worker_url"] != want.WorkerURL || fields["token"] != want.Token || fields["ca_sha256"] != want.CASHA256 {
				t.Errorf("the object is %v", fields)
			}
			if _, has := fields["ca_sha256"]; has != (want.CASHA256 != "") {
				t.Errorf("ca_sha256 is present: %v, and the string has a fingerprint: %v", has, want.CASHA256 != "")
			}
			got, err := api.ParseJoinString(text)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("the string reads back as %+v, and was %+v", got, want)
			}
		})
	}
}

// What a worker reads is normal: the URL in its one form, the fingerprint in lowercase, and the string with the
// space a copy and a paste adds taken off.
func TestAJoinStringIsReadAsAWorkerKeepsIt(t *testing.T) {
	t.Parallel()
	text := join(t, map[string]any{"worker_url": "https://Ropeshift.LAN:7400/", "ca_sha256": strings.ToUpper(fingerprint), "token": secret})
	got, err := api.ParseJoinString("  \t" + text + " \r\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkerURL != "https://ropeshift.lan:7400" || got.CASHA256 != fingerprint || got.Token != secret {
		t.Errorf("the string reads as %+v", got)
	}
	got, err = api.ParseJoinString(join(t, map[string]any{"worker_url": "https://ropeshift.example", "token": secret}))
	if err != nil || got.WorkerURL != "https://ropeshift.example:443" {
		t.Errorf("a URL with no port reads as %+v, %v", got, err)
	}
	got, err = api.ParseJoinString(join(t, map[string]any{"worker_url": "http://localhost:7400", "token": secret}))
	if err != nil || got.WorkerURL != "http://127.0.0.1:7400" {
		t.Errorf("a URL of localhost reads as %+v, %v", got, err)
	}
}

// A string that is not one a worker can use is refused, whatever it holds, and what it holds is never in the
// refusal: a join string is a credential until it is spent, and an error ends in logs and terminals.
func TestAJoinStringThatIsNoUseIsRefusedWithoutRepeatingIt(t *testing.T) {
	t.Parallel()
	good := map[string]any{"worker_url": "https://ropeshift.lan:7400", "ca_sha256": fingerprint, "token": secret}
	with := func(change func(map[string]any)) string {
		fields := map[string]any{}
		for name, value := range good {
			fields[name] = value
		}
		change(fields)
		return join(t, fields)
	}
	whole := with(func(map[string]any) {})
	object := func(text string) string {
		return "ropeshift-join-1." + base64.RawURLEncoding.EncodeToString([]byte(text))
	}
	tests := []struct{ name, text string }{
		{"nothing", ""},
		{"spaces", "   "},
		{"a bare join token", secret},
		{"a token with its parts", "abcd.efgh"},
		{"another version", strings.Replace(whole, "ropeshift-join-1.", "ropeshift-join-2.", 1)},
		{"no version", strings.TrimPrefix(whole, "ropeshift-join-1.")},
		{"the prefix alone", "ropeshift-join-1."},
		{"padded base64", whole + "="},
		{"standard base64", "ropeshift-join-1." + base64.StdEncoding.EncodeToString([]byte(`{"worker_url":"https://a.lan","token":"`+secret+`?>>"}`))},
		{"base64 that is no JSON", object("token=" + secret)},
		{"JSON that is no object", object(`["` + secret + `"]`)},
		{"a field nobody knows", with(func(f map[string]any) { f["extra"] = "x" })},
		{"two objects", object(`{"worker_url":"https://a.lan","token":"` + secret + `"}{}`)},
		{"no token", with(func(f map[string]any) { delete(f, "token") })},
		{"an empty token", with(func(f map[string]any) { f["token"] = "" })},
		{"no worker URL", with(func(f map[string]any) { delete(f, "worker_url") })},
		{"a worker URL that is no URL", with(func(f map[string]any) { f["worker_url"] = "ropeshift.lan:7400" })},
		{"a worker URL over ftp", with(func(f map[string]any) { f["worker_url"] = "ftp://ropeshift.lan" })},
		{"http off loopback", with(func(f map[string]any) { f["worker_url"] = "http://ropeshift.lan:7400"; delete(f, "ca_sha256") })},
		{"http on a private IP", with(func(f map[string]any) { f["worker_url"] = "http://192.168.1.5:7400"; delete(f, "ca_sha256") })},
		{"a fingerprint on http", with(func(f map[string]any) { f["worker_url"] = "http://127.0.0.1:7400" })},
		{"credentials in the URL", with(func(f map[string]any) { f["worker_url"] = "https://me:" + secret + "@ropeshift.lan" })},
		{"a path in the URL", with(func(f map[string]any) { f["worker_url"] = "https://ropeshift.lan/" + secret })},
		{"a short fingerprint", with(func(f map[string]any) { f["ca_sha256"] = fingerprint[:63] })},
		{"a long fingerprint", with(func(f map[string]any) { f["ca_sha256"] = fingerprint + "0" })},
		{"a fingerprint that is no hex", with(func(f map[string]any) { f["ca_sha256"] = strings.Repeat("z", 64) })},
		{"an empty fingerprint", with(func(f map[string]any) { f["ca_sha256"] = "" })},
		{"a string too long", with(func(f map[string]any) { f["token"] = strings.Repeat("t", 5000) })},
		{"a token with a newline", with(func(f map[string]any) { f["token"] = secret + "\nmore" })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := api.ParseJoinString(tt.text)
			if err == nil {
				t.Fatalf("the string is taken as %+v", got)
			}
			if got != (api.JoinString{}) {
				t.Errorf("a refused string reads as %+v", got)
			}
			for _, part := range []string{secret, fingerprint, strings.ToUpper(fingerprint), "ropeshift.lan", strings.TrimPrefix(tt.text, api.JoinPrefix)} {
				if part != "" && strings.Contains(err.Error(), part) {
					t.Errorf("the refusal repeats %q: %v", part, err)
				}
			}
		})
	}
}

// The string is built from the same rules it is read by, so a server that was given a URL it could not have read
// back makes no join string.
func TestAJoinStringIsEncodedOnlyWhenAWorkerCouldReadIt(t *testing.T) {
	t.Parallel()
	for _, j := range []api.JoinString{
		{WorkerURL: "https://ropeshift.lan:7400"},
		{Token: secret},
		{WorkerURL: "http://ropeshift.lan:7400", Token: secret},
		{WorkerURL: "https://ropeshift.lan:7400", CASHA256: "abc", Token: secret},
		{WorkerURL: "http://127.0.0.1:7400", CASHA256: fingerprint, Token: secret},
	} {
		if text, err := j.Encode(); err == nil {
			t.Errorf("%q is made", text)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("the refusal repeats the token: %v", err)
		}
	}
}

// A join string is a credential, so printing one by accident prints no part of it.
func TestAJoinStringStructPrintsNothingOfWhatItHolds(t *testing.T) {
	t.Parallel()
	j := api.JoinString{WorkerURL: "https://ropeshift.lan:7400", CASHA256: fingerprint, Token: secret}
	for _, shown := range []string{fmt.Sprint(j), fmt.Sprintf("%v", j), fmt.Sprintf("%+v", j), fmt.Sprintf("%#v", j), fmt.Sprintf("[%s]", j), fmt.Sprint(&j)} {
		for _, part := range []string{secret, fingerprint} {
			if strings.Contains(shown, part) {
				t.Errorf("%q shows %q", shown, part)
			}
		}
	}
}
