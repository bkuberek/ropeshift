package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ForgeGitHub is the one forge ropeshift creates a project's repositories on.
const ForgeGitHub = "github"

// GitHubHost is the host ropeshift creates repositories on: github.com only.
const GitHubHost = "github.com"

// The visibilities of a repository ropeshift creates. An absent visibility is private.
const (
	VisibilityPrivate = "private"
	VisibilityPublic  = "public"
)

// The steps a worker runs to give a repository its remote, in order, and their outcomes.
const (
	RemoteStepCreate = "create"
	RemoteStepOrigin = "origin"
	RemoteStepPush   = "push"

	RemoteStepDone    = "done"
	RemoteStepFailed  = "failed"
	RemoteStepSkipped = "skipped"
)

// The reasons a worker refuses a template write whose remote it cannot make, besides the folder's.
const (
	TemplateRefusedGHMissing    = "gh_missing"
	TemplateRefusedGHOutdated   = "gh_outdated"
	TemplateRefusedGHSignedOut  = "gh_signed_out"
	TemplateRefusedGHScope      = "gh_scope_missing"
	TemplateRefusedOwner        = "owner_not_available"
	TemplateRefusedRemoteExists = "remote_exists"
	// TemplateRefusedGHTimeout is a check of a repository a plan approval creates that GitHub did not answer within
	// the check's limit.
	TemplateRefusedGHTimeout = "gh_timeout"
	// TemplateRefusedNoOwnRepository is a request for a remote on a folder that is no repository of its own: a clone
	// that is not the worker's to change holds it.
	TemplateRefusedNoOwnRepository = "remote_needs_repository"
)

// The states of a forge account read, which says whether the worker's `gh` can create repositories.
const (
	ForgeAccountOK        = "ok"
	ForgeAccountMissing   = "gh_missing"
	ForgeAccountOutdated  = "gh_outdated"
	ForgeAccountSignedOut = "gh_signed_out"
	ForgeAccountScope     = "gh_scope_missing"
)

// ProtocolProjectRemotes is the oldest protocol a worker is offered a request that holds a remote, and a forge
// account read, on. An older worker drops the remote fields of a template write and would write the files with no
// remote, and drops desired state's forge_account_reads and would never report one.
const ProtocolProjectRemotes = 6

// ErrInvalidProjectRemote is the error of a remote no worker could be asked to make.
var ErrInvalidProjectRemote = errors.New("invalid project remote")

// RemoteCreate asks that a repository be created on a forge under an owner.
type RemoteCreate struct {
	// Forge is github, the only forge ropeshift creates repositories on.
	Forge string `json:"forge"`
	// Owner is the account or organization that holds the repository.
	Owner string `json:"owner"`
	// Visibility is private or public, and private when absent.
	Visibility string `json:"visibility,omitempty"`
}

// Public says the repository is created public: only the visibility public says so.
func (c RemoteCreate) Public() bool { return c.Visibility == VisibilityPublic }

// ProjectRemote is the remote of one repository of a new project: absent for none, a URL to add as origin, or a
// repository to create on a forge. It holds exactly one of the two.
type ProjectRemote struct {
	URL    string        `json:"url,omitempty"`
	Create *RemoteCreate `json:"create,omitempty"`
}

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9])*$`)

// CheckOwner refuses a name that is no GitHub account name: letters, digits, and single hyphens, at most 39
// characters, not starting or ending with a hyphen.
func CheckOwner(owner string) error {
	if len(owner) == 0 || len(owner) > 39 || !ownerPattern.MatchString(owner) {
		return fmt.Errorf("%w: %q is no GitHub account name: use letters, digits, and single hyphens, at most 39 characters, not starting or ending with a hyphen", ErrInvalidProjectRemote, owner)
	}
	return nil
}

// UnsupportedForgeError is a request to create a repository on a forge other than GitHub.
type UnsupportedForgeError struct{ Forge string }

func (e *UnsupportedForgeError) Error() string {
	return fmt.Sprintf("creating a repository on %s is not supported yet; ropeshift creates repositories on GitHub", e.Forge)
}

// ParseForgeOwner reads github:<owner>, and a repository created under it is private. Any other forge is an
// UnsupportedForgeError.
func ParseForgeOwner(value string) (*RemoteCreate, error) {
	forge, owner, found := strings.Cut(value, ":")
	if !found || forge == "" || owner == "" {
		return nil, fmt.Errorf("%q is not github:<owner>", value)
	}
	if forge != ForgeGitHub {
		return nil, &UnsupportedForgeError{Forge: forge}
	}
	if err := CheckOwner(owner); err != nil {
		return nil, err
	}
	return &RemoteCreate{Forge: forge, Owner: owner, Visibility: VisibilityPrivate}, nil
}

// maxForgeRepositoryName is the longest name a GitHub repository has.
const maxForgeRepositoryName = 100

// CheckForgeRepositoryName refuses a name GitHub cannot give a repository: one of 1 to 100 letters, digits, dots,
// underscores, and hyphens, which is neither "." nor "..".
func CheckForgeRepositoryName(name string) error {
	if name == "" || len(name) > maxForgeRepositoryName || name == "." || name == ".." || strings.ContainsFunc(name, func(r rune) bool { return !forgeNameRune(r) }) {
		return fmt.Errorf("a GitHub repository name has 1 to 100 letters, digits, dots, underscores, and hyphens and is not . or .., and %q is not one", name)
	}
	return nil
}

func forgeNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// CheckURL refuses a remote URL that is empty, holds whitespace, or starts with a dash, which git would read as an
// option.
func CheckURL(url string) error {
	switch {
	case url == "":
		return fmt.Errorf("%w: a remote URL is empty", ErrInvalidProjectRemote)
	case strings.IndexFunc(url, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0:
		return fmt.Errorf("%w: a remote URL holds whitespace", ErrInvalidProjectRemote)
	case strings.HasPrefix(url, "-"):
		return fmt.Errorf("%w: a remote URL does not start with a dash", ErrInvalidProjectRemote)
	}
	return nil
}

// Check refuses a remote that names both or neither of a URL and a repository to create, a URL CheckURL refuses, a
// forge other than github, an owner CheckOwner refuses, and a visibility other than the two.
func (r ProjectRemote) Check() error {
	switch {
	case r.URL != "" && r.Create != nil:
		return fmt.Errorf("%w: a remote names a url or a repository to create, not both", ErrInvalidProjectRemote)
	case r.URL == "" && r.Create == nil:
		return fmt.Errorf("%w: a remote names a url or a repository to create", ErrInvalidProjectRemote)
	case r.Create == nil:
		return CheckURL(r.URL)
	}
	return r.Create.Check()
}

// Check refuses a forge other than github, an owner that is no account name, and a visibility other than private
// or public.
func (c RemoteCreate) Check() error {
	if c.Forge != ForgeGitHub {
		return fmt.Errorf("%w: %w", ErrInvalidProjectRemote, &UnsupportedForgeError{Forge: c.Forge})
	}
	if err := CheckOwner(c.Owner); err != nil {
		return err
	}
	if c.Visibility != "" && c.Visibility != VisibilityPrivate && c.Visibility != VisibilityPublic {
		return fmt.Errorf("%w: a visibility is private or public, and %q is neither", ErrInvalidProjectRemote, c.Visibility)
	}
	return nil
}

// CreatedURL is the URL of a repository ropeshift created on GitHub, as the person's gh git protocol names it:
// the scp-like SSH address for ssh, and the HTTPS URL otherwise.
func CreatedURL(gitProtocol, owner, name string) string {
	if gitProtocol == "ssh" {
		return "git@" + GitHubHost + ":" + owner + "/" + name + ".git"
	}
	return "https://" + GitHubHost + "/" + owner + "/" + name + ".git"
}

// RemoteStep is one step of giving a repository its remote, with how it ended and what the tool said.
type RemoteStep struct {
	Step    string `json:"step"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// RepositoryReport is what a worker did for one repository it made: the folder's own repository, at path ".", or a
// new code repository, at repos/<name>.
type RepositoryReport struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Commit      string       `json:"commit,omitempty"`
	CommitError string       `json:"commit_error,omitempty"`
	Remote      string       `json:"remote,omitempty"`
	Steps       []RemoteStep `json:"steps,omitempty"`
}

// ForgeAccountReadTTLMs is how long a forge account read is offered: two minutes after a member asked.
const ForgeAccountReadTTLMs int64 = 2 * 60 * 1000

// DesiredForgeAccountRead is a forge account read a worker has not reported.
type DesiredForgeAccountRead struct {
	ID        string `json:"id"`
	Forge     string `json:"forge"`
	ExpiresAt int64  `json:"expires_at"`
}

// ForgeAccountReport is what a worker's `gh` can do for a member: whether it can create repositories, the account
// it is signed in as, its git protocol, and the owners a repository may be created under.
type ForgeAccountReport struct {
	Auth    WorkerAuth `json:"-"`
	Request string     `json:"request"`
	// State is ok, gh_missing, gh_outdated, gh_signed_out, or gh_scope_missing.
	State       string `json:"state"`
	Login       string `json:"login,omitempty"`
	GitProtocol string `json:"git_protocol,omitempty"`
	// Owners are the login first, and then the organizations, sorted.
	Owners []string `json:"owners,omitempty"`
	Detail string   `json:"detail,omitempty"`
	// Remedy is the command a person runs on the worker to fix what the state names.
	Remedy string `json:"remedy,omitempty"`
}

// ForgeAccountReadState is where a forge account read stands.
type ForgeAccountReadState string

// The states of a forge account read: offered until the worker reports, then reported, and expired when nobody
// reported by its expiry.
const (
	ForgeAccountReadOffered  ForgeAccountReadState = "offered"
	ForgeAccountReadReported ForgeAccountReadState = "reported"
	ForgeAccountReadExpired  ForgeAccountReadState = "expired"
)

// ForgeAccountReadRecord is a forge account read as the installation holds it.
type ForgeAccountReadRecord struct {
	ID          string
	Worker      string
	RequestedBy string
	Forge       string
	RequestedAt int64
	ExpiresAt   int64
	State       ForgeAccountReadState
	Report      *ForgeAccountReport
	ReportedAt  int64
}

// StateAt is the read's state at a time: offered becomes expired once its expiry passes with no report.
func (r ForgeAccountReadRecord) StateAt(now int64) ForgeAccountReadState {
	if r.State == ForgeAccountReadOffered && now >= r.ExpiresAt {
		return ForgeAccountReadExpired
	}
	return r.State
}

// Desired is the read as its worker reads it.
func (r ForgeAccountReadRecord) Desired() DesiredForgeAccountRead {
	return DesiredForgeAccountRead{ID: r.ID, Forge: r.Forge, ExpiresAt: r.ExpiresAt}
}

// ForgeAccountReadView is a forge account read as a member reads it.
type ForgeAccountReadView struct {
	ID          string                `json:"id"`
	Worker      string                `json:"worker"`
	RequestedBy string                `json:"requested_by"`
	RequestedAt int64                 `json:"requested_at"`
	ExpiresAt   int64                 `json:"expires_at"`
	Forge       string                `json:"forge"`
	State       ForgeAccountReadState `json:"state"`
	Report      *ForgeAccountReport   `json:"report,omitempty"`
	ReportedAt  int64                 `json:"reported_at,omitempty"`
}

// ViewAt is the record as a member reads it at a time.
func (r ForgeAccountReadRecord) ViewAt(now int64) ForgeAccountReadView {
	return ForgeAccountReadView{
		ID: r.ID, Worker: r.Worker, RequestedBy: r.RequestedBy, RequestedAt: r.RequestedAt, ExpiresAt: r.ExpiresAt,
		Forge: r.Forge, State: r.StateAt(now), Report: r.Report, ReportedAt: r.ReportedAt,
	}
}

// ForgeAccountRequest is the body of a member's request that a worker read its forge account.
type ForgeAccountRequest struct {
	Forge string `json:"forge"`
}

// Check refuses a forge other than github.
func (r ForgeAccountRequest) Check() error {
	if r.Forge != ForgeGitHub {
		return fmt.Errorf("%w: reading an account on %s is not supported yet; ropeshift creates repositories on GitHub", ErrInvalidProjectRemote, r.Forge)
	}
	return nil
}
