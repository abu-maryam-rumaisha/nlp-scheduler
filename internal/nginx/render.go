package nginx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// validateValue rejects variable values that could escape the argument or
// block they are substituted into.
func validateValue(v string) error {
	if strings.ContainsAny(v, "\r\n;{}\"'\\") {
		return fmt.Errorf("value %q must not contain newlines, quotes, backslashes or any of ;{}", v)
	}
	return nil
}

// Placeholders returns the sorted, de-duplicated placeholder names used
// anywhere in the tree.
func Placeholders(ds []Directive) []string {
	seen := map[string]bool{}
	var walk func([]Directive)
	collect := func(s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			seen[m[1]] = true
		}
	}
	walk = func(ds []Directive) {
		for _, d := range ds {
			for _, a := range d.Args {
				collect(a)
			}
			if d.Raw != nil {
				collect(*d.Raw)
			}
			walk(d.Children)
		}
	}
	walk(ds)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveVariables merges request values over declared defaults and checks
// that every placeholder used by the tree has a value.
func ResolveVariables(ds []Directive, decl []Variable, values map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(decl)+len(values))
	for _, v := range decl {
		if v.Default != nil {
			resolved[v.Name] = *v.Default
		}
	}
	for k, v := range values {
		if !variableNameRe.MatchString(k) {
			return nil, invalid("variables", "invalid variable name %q", k)
		}
		if err := validateValue(v); err != nil {
			return nil, invalid("variables."+k, "%s", err)
		}
		resolved[k] = v
	}

	var missing []string
	for _, v := range decl {
		if _, ok := resolved[v.Name]; !ok && v.Required {
			missing = append(missing, v.Name)
		}
	}
	for _, name := range Placeholders(ds) {
		if _, ok := resolved[name]; !ok && !contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, invalid("variables", "missing values for: %s", strings.Join(missing, ", "))
	}
	return resolved, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func substitute(s string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		return vars[name]
	})
}

// Render produces configuration text. vars must already be resolved (see
// ResolveVariables); every argument is re-validated after substitution.
func Render(ds []Directive, vars map[string]string) (string, error) {
	var b strings.Builder
	if err := renderLevel(&b, ds, vars, 0, "directives"); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderLevel(b *strings.Builder, ds []Directive, vars map[string]string, depth int, path string) error {
	indent := strings.Repeat("    ", depth)
	for i, d := range ds {
		p := fmt.Sprintf("%s[%d]", path, i)
		if d.Comment != "" {
			fmt.Fprintf(b, "%s# %s\n", indent, d.Comment)
		}
		b.WriteString(indent)
		b.WriteString(d.Name)
		for j, a := range d.Args {
			a = substitute(a, vars)
			if err := validateArg(a); err != nil {
				return invalid(fmt.Sprintf("%s.args[%d]", p, j), "after substitution: %s", err)
			}
			b.WriteByte(' ')
			b.WriteString(a)
		}
		switch {
		case d.Raw != nil:
			b.WriteString(" {\n")
			for _, line := range strings.Split(strings.Trim(substitute(*d.Raw, vars), "\n"), "\n") {
				if strings.TrimSpace(line) == "" {
					b.WriteString("\n")
					continue
				}
				b.WriteString(indent + "    " + line + "\n")
			}
			b.WriteString(indent + "}\n")
		case d.IsBlock():
			b.WriteString(" {\n")
			if err := renderLevel(b, d.Children, vars, depth+1, p+".children"); err != nil {
				return err
			}
			b.WriteString(indent + "}\n")
		default:
			b.WriteString(";\n")
		}
	}
	return nil
}
