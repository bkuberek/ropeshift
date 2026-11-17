package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

// A join string is how a member hands a worker everything it needs to join a server and trust it (ADR-0031): the
// URL the worker reaches the server at, the fingerprint of the server's certificate authority when the server is
// its own, and the join token. It is one opaque value, `ropeshift-join-1.` followed by the unpadded base64url of
// one JSON object, and it is a credential until its token is spent, so nothing here puts a field of it in an error,
// a log, or what a program prints when it is handed to one by mistake.

// JoinPrefix begins every join string, and its last digit is the format's version.
const JoinPrefix = "ropeshift-join-1."

const (
	joinPrefixFamily = "ropeshift-join-"
	// maxJoinString is the most a join string is, which a token, a URL, and a fingerprint come nowhere near.
	maxJoinString = 4096
	maxJoinToken  = 512
)

// JoinString is what a join string holds. Its fields are the credential, so printing one prints none of them.
type JoinString struct {
	// WorkerURL is where a worker reaches the server: an https:// URL, or an http:// URL of a loopback address.
	WorkerURL string
	// CASHA256 is the SHA-256 of the server's certificate authority in lowercase hex, which a worker pins. It is
	// empty when the worker verifies the server through the system's roots, and for an http:// URL.
	CASHA256 string
	// Token is the single-use join token.
	Token string
}

// String says nothing of what the join string holds, so a log line that prints one holds no credential.
func (JoinString) String() string { return "a join string" }

// GoString says nothing of what the join string holds either.
func (JoinString) GoString() string { return "api.JoinString{}" }

// joinFields is the object a join string holds. The fingerprint is a pointer so that a string that holds an
// empty one is no string that holds none.
type joinFields struct {
	WorkerURL string  `json:"worker_url"`
	CASHA256  *string `json:"ca_sha256,omitempty"`
	Token     string  `json:"token"`
}

// CheckWorkerURL says whether a worker may reach a server at a URL, and returns it in the one form a worker keeps:
// an https:// URL with its host in lowercase and its port, or an http:// URL of a loopback address. Its errors name
// what is wrong and carry none of the URL.
func CheckWorkerURL(raw string) (string, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "https://"):
		return SecureServerAddress(raw)
	case strings.HasPrefix(strings.ToLower(raw), "http://"):
		return ServerAddress(raw)
	}
	return "", errors.New("the worker URL is an https:// URL, or an http:// URL of a loopback address")
}

// check refuses a join string no worker could use, and returns it in the form a worker keeps: the URL in its one
// form, and the fingerprint in lowercase. Its errors name the field and never repeat it.
func (j JoinString) check() (JoinString, error) {
	if j.Token == "" || len(j.Token) > maxJoinToken || strings.IndexFunc(j.Token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return JoinString{}, errors.New("the join string holds no usable join token")
	}
	var err error
	if j.WorkerURL, err = CheckWorkerURL(j.WorkerURL); err != nil {
		return JoinString{}, errors.New("the join string's worker URL is not one a worker reaches a server at: " + err.Error())
	}
	if j.CASHA256 != "" {
		if !strings.HasPrefix(j.WorkerURL, "https://") {
			return JoinString{}, errors.New("the join string pins a certificate authority for a URL that has none")
		}
		raw, err := hex.DecodeString(j.CASHA256)
		if err != nil || len(raw) != 32 {
			return JoinString{}, errors.New("the join string's fingerprint is not the SHA-256 of a certificate authority in hexadecimal")
		}
		j.CASHA256 = hex.EncodeToString(raw)
	}
	return j, nil
}

// Encode makes the join string. It refuses what a worker would refuse, so a server never makes one that fails
// at the worker's end.
func (j JoinString) Encode() (string, error) {
	checked, err := j.check()
	if err != nil {
		return "", err
	}
	fields := joinFields{WorkerURL: checked.WorkerURL, Token: checked.Token}
	if checked.CASHA256 != "" {
		fields.CASHA256 = &checked.CASHA256
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return JoinPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ParseJoinString reads a join string, with the space around it a copy and a paste adds taken off. A string that
// is not one is refused with a sentence that says what kind of wrong it is and holds none of the string.
func ParseJoinString(text string) (JoinString, error) {
	text = strings.TrimSpace(text)
	switch {
	case len(text) > maxJoinString:
		return JoinString{}, errors.New("this is no join string: it is longer than any join string is")
	case !strings.HasPrefix(text, joinPrefixFamily):
		return JoinString{}, errors.New("this is no join string: it does not start with the prefix of one (" + strings.TrimSuffix(JoinPrefix, ".") + ")")
	case !strings.HasPrefix(text, JoinPrefix):
		return JoinString{}, errors.New("this build reads join strings of version 1, and this one is of another: make a new one with the build the server runs")
	}
	body := strings.TrimPrefix(text, JoinPrefix)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil || len(raw) == 0 {
		return JoinString{}, errors.New("this join string is damaged: copy it again whole")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fields joinFields
	if err := decoder.Decode(&fields); err != nil {
		return JoinString{}, errors.New("this join string is damaged: copy it again whole")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return JoinString{}, errors.New("this join string is damaged: copy it again whole")
	}
	got := JoinString{WorkerURL: fields.WorkerURL, Token: fields.Token}
	if fields.CASHA256 != nil {
		if *fields.CASHA256 == "" {
			return JoinString{}, errors.New("the join string's fingerprint is empty")
		}
		got.CASHA256 = *fields.CASHA256
	}
	return got.check()
}
