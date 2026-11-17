package server_test

import (
	"net/http"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/version"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
)

// FR-TPL-5: the route answers every template of the binary by name, sorted, each with the text Render returns
// over the example data, the shapes of `new` that write it, and the path it is written to.
func TestTheTemplatesRouteAnswersEveryTemplateAsTheBinaryRendersIt(t *testing.T) {
	t.Parallel()
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.StartWith(t, apitest.Options{Templates: list, Build: version.Info{Version: "v9.8.7"}})
	got, err := api.NewUnixClient(d.Socket).Templates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v9.8.7" {
		t.Errorf("version %q, want the daemon's build version", got.Version)
	}
	names := templates.Names()
	if len(got.Templates) != len(names) || len(names) < 20 {
		t.Fatalf("%d templates for %d names", len(got.Templates), len(names))
	}
	byShape := map[string][]projectfiles.Shape{}
	paths := map[string]string{}
	for _, shape := range projectfiles.Shapes() {
		data := projectfiles.Data{Project: "acme"}
		if shape == projectfiles.Workspace {
			data.Repositories = []projectfiles.CodeRepository{{Name: "acme-api", Remote: "git@example.invalid:acme/acme-api.git"}}
		}
		files, err := projectfiles.Files(shape, data, func(name string, _ projectfiles.Data) (string, error) { return name, nil })
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			byShape[file.Template] = append(byShape[file.Template], shape)
			paths[file.Template] = file.Path
		}
	}
	for i, name := range names {
		entry := got.Templates[i]
		if entry.Name != name {
			t.Errorf("entry %d is %q, want %q", i, entry.Name, name)
			continue
		}
		want, err := templates.Render(name, templates.Data{Example: true})
		if err != nil {
			t.Fatal(err)
		}
		if entry.Content != want {
			t.Errorf("%s: content is %d bytes that are not what Render returns, %d bytes", name, len(entry.Content), len(want))
		}
		var shapes []string
		for _, shape := range byShape[name] {
			shapes = append(shapes, string(shape))
		}
		if !slices.Equal(entry.Shapes, shapes) {
			t.Errorf("%s: shapes %v, want %v", name, entry.Shapes, shapes)
		}
		wantPath := paths[name]
		if name == "user/profile.toml" {
			wantPath = "profile.toml"
		}
		if entry.WritesTo != wantPath {
			t.Errorf("%s: writes to %q, want %q", name, entry.WritesTo, wantPath)
		}
	}
}

// A template no shape writes lists its shapes as an empty array, and not as null, so a reader needs no check.
func TestTheTemplatesRouteListsNoShapesAsAnEmptyArray(t *testing.T) {
	t.Parallel()
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.StartWith(t, apitest.Options{Templates: list})
	var raw struct {
		Templates []map[string]any `json:"templates"`
	}
	if err := api.NewUnixClient(d.Socket).Do(t.Context(), http.MethodGet, api.TemplatesPath, nil, &raw); err != nil {
		t.Fatal(err)
	}
	var checked int
	for _, entry := range raw.Templates {
		if entry["name"] == "dependabot.yml" {
			shapes, ok := entry["shapes"].([]any)
			if !ok || len(shapes) != 0 {
				t.Errorf("dependabot.yml lists shapes %#v", entry["shapes"])
			}
			checked++
		}
	}
	if checked != 1 {
		t.Errorf("dependabot.yml is listed %d times", checked)
	}
}

// With no list passed, the route refuses as unavailable, and says what the daemon lacks.
func TestTheTemplatesRouteRefusesWithNoListPassed(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	_, err := api.NewUnixClient(d.Socket).Templates(t.Context())
	refusedWith(t, err, http.StatusNotImplemented, api.CodeUnavailable)
}

// The route is a read: a request with no session is answered, as /v1/workers is on the socket.
func TestTheTemplatesRouteNeedsNoSession(t *testing.T) {
	t.Parallel()
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.StartWith(t, apitest.Options{Templates: list, NoMember: true})
	if _, err := api.NewUnixClient(d.Socket).Templates(t.Context()); err != nil {
		t.Fatalf("a read with no session: %v", err)
	}
}
