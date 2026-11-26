package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Samples read the address and the credentials from the environment, so a page
// never carries a token and never picks between sandbox and production.
const (
	baseURLVariable = "API_BASE_URL"
	tokenVariable   = "API_ACCESS_TOKEN"
	keyVariable     = "API_KEY"
	otherAuthNote   = "Add the credentials this operation requires; see the security schemes in the source bundle."
)

type sample struct {
	Language, Code string
}

// Every string in a request comes from the contract and is quoted for the
// language it is written in, so a copied sample runs nothing it does not show.
type request struct {
	method, target    string
	headers           [][2]string
	credentials       []credential
	otherAuth         bool
	contentType, body string
}

type credential struct {
	header, prefix, variable string
}

func operationRequest(doc *openapi3.T, method, path string, parameters []*openapi3.Parameter, op *openapi3.Operation) request {
	r := request{method: method, target: path}
	var query []string
	for _, p := range parameters {
		if !p.Required {
			continue
		}
		placeholder := "{" + p.Name + "}"
		switch p.In {
		case openapi3.ParameterInQuery:
			query = append(query, url.QueryEscape(p.Name)+"="+placeholder)
		case openapi3.ParameterInHeader:
			// OpenAPI ignores header parameters with these names.
			if !slices.ContainsFunc([]string{"Accept", "Content-Type", "Authorization"}, func(name string) bool {
				return strings.EqualFold(name, p.Name)
			}) {
				r.headers = append(r.headers, [2]string{p.Name, placeholder})
			}
		}
	}
	if len(query) > 0 {
		r.target += "?" + strings.Join(query, "&")
	}
	r.credentials, r.otherAuth = operationCredentials(doc, op)
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		r.contentType, r.body = jsonBody(op.RequestBody.Value.Content)
	}
	return r
}

func operationCredentials(doc *openapi3.T, op *openapi3.Operation) ([]credential, bool) {
	requirements := doc.Security
	if op.Security != nil {
		requirements = *op.Security
	}
	if len(requirements) == 0 || slices.ContainsFunc(requirements, func(r openapi3.SecurityRequirement) bool { return len(r) == 0 }) {
		return nil, false
	}
	var found []credential
	other := false
	for _, name := range slices.Sorted(maps.Keys(requirements[0])) {
		var scheme *openapi3.SecurityScheme
		if doc.Components != nil && doc.Components.SecuritySchemes[name] != nil {
			scheme = doc.Components.SecuritySchemes[name].Value
		}
		var next credential
		switch {
		case scheme == nil:
			other = true
			continue
		case scheme.Type == "oauth2" || scheme.Type == "openIdConnect" || scheme.Type == "http" && strings.EqualFold(scheme.Scheme, "bearer"):
			next = credential{header: "Authorization", prefix: "Bearer ", variable: tokenVariable}
		case scheme.Type == "apiKey" && scheme.In == "header" && scheme.Name != "":
			next = credential{header: scheme.Name, variable: keyVariable}
		default:
			other = true
			continue
		}
		if !slices.ContainsFunc(found, func(c credential) bool { return strings.EqualFold(c.header, next.header) }) {
			found = append(found, next)
		}
	}
	return found, other
}

// jsonBody picks the first JSON media type and sends its example, else a value
// built from its schema. Other media types get no sample body.
func jsonBody(content openapi3.Content) (string, string) {
	for _, name := range slices.Sorted(maps.Keys(content)) {
		media := content[name]
		if media == nil || !isJSON(name) {
			continue
		}
		var value any
		switch {
		case media.Example != nil:
			value = media.Example
		case len(media.Examples) > 0:
			for _, key := range slices.Sorted(maps.Keys(media.Examples)) {
				if example := media.Examples[key]; example != nil && example.Value != nil && example.Value.Value != nil {
					value = example.Value.Value
					break
				}
			}
		}
		if value == nil && media.Schema != nil && media.Schema.Value != nil {
			budget := schemaFields
			value = sampleValue(media.Schema.Value, 0, map[*openapi3.Schema]bool{}, &budget)
		}
		if value == nil {
			return "", ""
		}
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return "", ""
		}
		return name, strings.TrimSuffix(out.String(), "\n")
	}
	return "", ""
}

func isJSON(mediaType string) bool {
	base, _, _ := strings.Cut(mediaType, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	return base == "application/json" || strings.HasSuffix(base, "+json")
}

func codeSamples(r request) []sample {
	return []sample{{"cURL", curlSample(r)}, {"Go", goSample(r)}, {"TypeScript", typeScriptSample(r)}}
}

func curlSample(r request) string {
	var b strings.Builder
	if r.otherAuth {
		b.WriteString("# " + otherAuthNote + "\n")
	}
	b.WriteString("curl ")
	if strings.ContainsAny(r.target, "{}[]") {
		b.WriteString("--globoff ")
	}
	fmt.Fprintf(&b, "--request %s \"$%s\"%s", r.method, baseURLVariable, shellQuote(r.target))
	for _, c := range r.credentials {
		fmt.Fprintf(&b, " \\\n  --header %s\"$%s\"", shellQuote(c.header+": "+c.prefix), c.variable)
	}
	for _, h := range r.headers {
		fmt.Fprintf(&b, " \\\n  --header %s", shellQuote(h[0]+": "+h[1]))
	}
	if r.body != "" {
		fmt.Fprintf(&b, " \\\n  --header %s \\\n  --data %s", shellQuote("Content-Type: "+r.contentType), shellQuote(r.body))
	}
	return b.String()
}

func goSample(r request) string {
	var b strings.Builder
	imports := []string{"fmt", "io", "net/http", "os"}
	if r.body != "" {
		imports = append(imports, "strings")
	}
	b.WriteString("package main\n\nimport (\n")
	for _, name := range imports {
		fmt.Fprintf(&b, "\t%q\n", name)
	}
	b.WriteString(")\n\nfunc main() {\n")
	if r.otherAuth {
		b.WriteString("\t// " + otherAuthNote + "\n")
	}
	body := "nil"
	if r.body != "" {
		fmt.Fprintf(&b, "\tbody := strings.NewReader(%s)\n", goString(r.body))
		body = "body"
	}
	fmt.Fprintf(&b, "\trequest, err := http.NewRequest(%q, os.Getenv(%q)+%s, %s)\n", r.method, baseURLVariable, strconv.Quote(r.target), body)
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	for _, c := range r.credentials {
		value := fmt.Sprintf("os.Getenv(%q)", c.variable)
		if c.prefix != "" {
			value = strconv.Quote(c.prefix) + "+" + value
		}
		fmt.Fprintf(&b, "\trequest.Header.Set(%s, %s)\n", strconv.Quote(c.header), value)
	}
	for _, h := range r.headers {
		fmt.Fprintf(&b, "\trequest.Header.Set(%s, %s)\n", strconv.Quote(h[0]), strconv.Quote(h[1]))
	}
	if r.body != "" {
		fmt.Fprintf(&b, "\trequest.Header.Set(\"Content-Type\", %s)\n", strconv.Quote(r.contentType))
	}
	b.WriteString("\tresponse, err := http.DefaultClient.Do(request)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n" +
		"\tdefer response.Body.Close()\n\tdata, err := io.ReadAll(response.Body)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n" +
		"\tfmt.Println(response.Status)\n\tfmt.Println(string(data))\n}\n")
	return b.String()
}

func typeScriptSample(r request) string {
	var b strings.Builder
	if r.otherAuth {
		b.WriteString("// " + otherAuthNote + "\n")
	}
	fmt.Fprintf(&b, "const response = await fetch(process.env.%s + %s, {\n  method: %s,\n", baseURLVariable, jsString(r.target), jsString(r.method))
	if len(r.credentials)+len(r.headers) > 0 || r.body != "" {
		b.WriteString("  headers: {\n")
		for _, c := range r.credentials {
			value := "process.env." + c.variable
			if c.prefix != "" {
				value = jsString(c.prefix) + " + " + value
			}
			fmt.Fprintf(&b, "    %s: %s,\n", jsString(c.header), value)
		}
		for _, h := range r.headers {
			fmt.Fprintf(&b, "    %s: %s,\n", jsString(h[0]), jsString(h[1]))
		}
		if r.body != "" {
			fmt.Fprintf(&b, "    \"Content-Type\": %s,\n", jsString(r.contentType))
		}
		b.WriteString("  },\n")
	}
	if r.body != "" {
		// JSON text is also a JavaScript expression that evaluates to the same value.
		fmt.Fprintf(&b, "  body: JSON.stringify(%s),\n", strings.ReplaceAll(r.body, "\n", "\n  "))
	}
	b.WriteString("});\nconsole.log(response.status, await response.text());\n")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func goString(s string) string {
	if strings.ContainsAny(s, "`\r") {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}

func jsString(s string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return strings.TrimSuffix(out.String(), "\n")
}
