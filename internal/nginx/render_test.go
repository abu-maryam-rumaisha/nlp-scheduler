package nginx

import (
	"encoding/json"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestRenderNestedWithVariables(t *testing.T) {
	tree := []Directive{
		{Name: "upstream", Args: []string{"backend"}, Children: []Directive{
			{Name: "server", Args: []string{"{{ backend_host }}:{{backend_port}}"}},
		}},
		{Name: "server", Comment: "public entrypoint", Children: []Directive{
			{Name: "listen", Args: []string{"{{port}}"}},
			{Name: "location", Args: []string{"/"}, Children: []Directive{
				{Name: "proxy_pass", Args: []string{"http://backend"}},
				{Name: "proxy_set_header", Args: []string{"Host", "$host"}},
			}},
			{Name: "location", Args: []string{"=", "/hello"}, Children: []Directive{
				{Name: "content_by_lua_block", Raw: ptr("ngx.say(\"hello from {{greeting}}\")")},
			}},
		}},
	}
	if err := ValidateTree(tree); err != nil {
		t.Fatal(err)
	}
	decl := []Variable{{Name: "port", Default: ptr("80")}, {Name: "greeting", Default: ptr("openresty")}}
	vars, err := ResolveVariables(tree, decl, map[string]string{"backend_host": "10.0.0.5", "backend_port": "8080"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(tree, vars)
	if err != nil {
		t.Fatal(err)
	}
	want := `upstream backend {
    server 10.0.0.5:8080;
}
# public entrypoint
server {
    listen 80;
    location / {
        proxy_pass http://backend;
        proxy_set_header Host $host;
    }
    location = /hello {
        content_by_lua_block {
            ngx.say("hello from openresty")
        }
    }
}
`
	if got != want {
		t.Fatalf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestEmptyBlockAndJSONShape(t *testing.T) {
	var tree []Directive
	err := json.Unmarshal([]byte(`[{"name":"events","block":true},{"name":"worker_processes","args":["auto"]}]`), &tree)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "events {\n}\nworker_processes auto;\n" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveVariablesMissing(t *testing.T) {
	tree := []Directive{{Name: "listen", Args: []string{"{{port}}"}}}
	_, err := ResolveVariables(tree, []Variable{{Name: "unused", Required: true}}, nil)
	if err == nil || !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "unused") {
		t.Fatalf("want missing port and unused, got %v", err)
	}
}

func TestInjectionRejected(t *testing.T) {
	tree := []Directive{{Name: "server_name", Args: []string{"{{name}}"}}}
	for _, v := range []string{"a; evil on", "a}", "a\nb", `"x"`} {
		if _, err := ResolveVariables(tree, nil, map[string]string{"name": v}); err == nil {
			t.Errorf("value %q accepted", v)
		}
	}
	// Whitespace is caught after substitution in an unquoted argument.
	vars, err := ResolveVariables(tree, nil, map[string]string{"name": "a b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(tree, vars); err == nil {
		t.Error("unquoted argument with a space rendered")
	}
}

func TestValidateTree(t *testing.T) {
	cases := map[string][]Directive{
		"bad name":           {{Name: "proxy-pass"}},
		"unquoted semicolon": {{Name: "return", Args: []string{"200;"}}},
		"raw and children":   {{Name: "x", Raw: ptr(""), Children: []Directive{{Name: "y"}}}},
		"nested bad arg":     {{Name: "http", Children: []Directive{{Name: "a", Args: []string{"{"}}}}},
		"multiline comment":  {{Name: "a", Comment: "x\ny"}},
	}
	for name, tree := range cases {
		if err := ValidateTree(tree); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := []Directive{{Name: "return", Args: []string{"200", `"ok; {fine}"`}}}
	if err := ValidateTree(ok); err != nil {
		t.Errorf("quoted argument rejected: %v", err)
	}
}
