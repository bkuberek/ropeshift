package lanes

// The kinds and statuses of a sign-in, as a driver reports them. A vendor's own words stop
// at ReadSignIn, so nothing past the driver depends on them.
const (
	SignInSubscription = "subscription"
	SignInAPIKey       = "api_key"

	SignedIn      = "signed_in"
	SignedOut     = "signed_out"
	SignInUnknown = "unknown"
)

// authMethods maps each documented `authMethod`. `third_party` is a cloud provider's own
// account, billed to whoever owns it, so it reads as a key and not as a person's plan.
var authMethods = map[string]string{
	"claude.ai": SignInSubscription, "oauth_token": SignInSubscription,
	"api_key": SignInAPIKey, "api_key_helper": SignInAPIKey, "third_party": SignInAPIKey,
}

// ReadSignIn turns a vendor's word into a kind and a status. A word nobody documented is
// unknown, which holds nothing back.
func ReadSignIn(method string) (kind, status string) {
	if method == "none" {
		return "", SignedOut
	}
	if kind, ok := authMethods[method]; ok {
		return kind, SignedIn
	}
	return "", SignInUnknown
}

// ReasonNoInteractiveMode is the reason a planning lane records when this machine cannot run it where
// a person can attach.
const ReasonNoInteractiveMode = "no_interactive_mode"

// DefaultRunMode says how a lane runs when no layer of its profile says. A subscription
// runs where a person can attach, because that is closest to ordinary use, and everything
// else runs headless.
func DefaultRunMode(kind string, interactive bool) string {
	if kind == SignInSubscription && interactive {
		return ProfileRunTmux
	}
	return ProfileRunHeadless
}
