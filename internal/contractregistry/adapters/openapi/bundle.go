package openapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "github.com/oasdiff/yaml3"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// The one version of each specification a revision is recorded in.
const (
	specVersion      = "3.1.2"
	eventSpecVersion = "3.1.0"

	maxNodes          = 8192
	maxNodeDepth      = 64
	maxReferences     = 512
	maxReferenceDepth = 16
	maxReferenceBytes = 1024
)

var (
	invalidPointerEscape = regexp.MustCompile(`~([^01]|$)`)
	unescapePointer      = strings.NewReplacer("~1", "/", "~0", "~")
)

// Capture checks the envelope of the entrypoint, OpenAPI or AsyncAPI, and
// resolves JSON Pointer references within the uploaded files. No filesystem
// or network loader is involved. Both are read the same way: their
// references are Reference Objects with a relative file path and a pointer.
func Capture(entrypoint string, files []domain.SourceFile) (domain.SourceSnapshot, error) {
	if !domain.ValidSourcePath(entrypoint) || len(files) < 1 || len(files) > domain.MaxSourceFiles {
		return domain.SourceSnapshot{}, invalid("the entrypoint or number of files is invalid")
	}
	bundle := &bundle{
		documents: make(map[string]*yaml.Node, len(files)),
		files:     make(map[string]domain.SourceFile, len(files)),
		mapping:   make(map[*yaml.Node]map[string]*yaml.Node),
		active:    make(map[*yaml.Node]bool),
		heights:   make(map[*yaml.Node]int),
		used:      map[string]bool{entrypoint: true},
		queue:     []string{entrypoint},
	}
	var byteCount int
	for _, file := range files {
		byteCount += len(file.Content)
		if byteCount > domain.MaxSourceBytes || !utf8.ValidString(file.Content) {
			return domain.SourceSnapshot{}, invalid("the bundle exceeds its byte budget or is not UTF-8")
		}
		if !domain.ValidSourcePath(file.Path) || bundle.documents[file.Path] != nil {
			return domain.SourceSnapshot{}, invalid("a file path is invalid or repeated")
		}
		root, err := readDocument(file)
		if err != nil {
			return domain.SourceSnapshot{}, err
		}
		if err := bundle.index(root, 0); err != nil {
			return domain.SourceSnapshot{}, err
		}
		bundle.documents[file.Path] = root
		bundle.files[file.Path] = file
	}
	root := bundle.mapping[bundle.documents[entrypoint]]
	specification, supported := domain.OpenAPI, specVersion
	version := root["openapi"]
	if event := root["asyncapi"]; event != nil {
		if version != nil {
			return domain.SourceSnapshot{}, invalid("the entrypoint declares both an OpenAPI and an AsyncAPI version")
		}
		specification, supported, version = domain.AsyncAPI, eventSpecVersion, event
	}
	if version == nil || version.ShortTag() != "!!str" || version.Kind != yaml.ScalarNode {
		return domain.SourceSnapshot{}, invalid("the entrypoint declares no OpenAPI or AsyncAPI version")
	}
	if version.Value != supported {
		return domain.SourceSnapshot{}, domain.ErrUnsupportedSpecVersion
	}
	info := bundle.mapping[root["info"]]
	if !nonemptyString(info["title"]) || !nonemptyString(info["version"]) {
		return domain.SourceSnapshot{}, invalid("the entrypoint needs a title and an API version")
	}

	// Include every file named by a reference, checking each captured file
	// in full since later checks receive its original bytes.
	closure := make([]domain.SourceFile, 0, len(files))
	for i := 0; i < len(bundle.queue); i++ {
		name := bundle.queue[i]
		if _, err := bundle.walk(name, bundle.documents[name], 0); err != nil {
			return domain.SourceSnapshot{}, err
		}
		closure = append(closure, bundle.files[name])
	}
	return domain.CaptureSource(entrypoint, specification, supported, closure)
}

func readDocument(file domain.SourceFile) (*yaml.Node, error) {
	if path.Ext(file.Path) == ".json" && !json.Valid([]byte(file.Content)) {
		return nil, invalid("a JSON file is not valid JSON")
	}
	decoder := yaml.NewDecoder(strings.NewReader(file.Content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || len(document.Content) != 1 {
		return nil, invalid("a file is not a YAML or JSON document")
	}
	if err := decoder.Decode(&yaml.Node{}); err != io.EOF {
		return nil, invalid("each file must contain one document")
	}
	return document.Content[0], nil
}

func nonemptyString(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.ShortTag() == "!!str" && strings.TrimSpace(node.Value) != ""
}

type bundle struct {
	documents map[string]*yaml.Node
	files     map[string]domain.SourceFile
	mapping   map[*yaml.Node]map[string]*yaml.Node
	nodes     int

	active     map[*yaml.Node]bool
	heights    map[*yaml.Node]int
	references int
	used       map[string]bool
	queue      []string
}

// index bounds the syntax tree and builds unambiguous pointer lookups.
func (b *bundle) index(node *yaml.Node, depth int) error {
	b.nodes++
	if depth > maxNodeDepth || b.nodes > maxNodes {
		return invalid("the documents exceed their nesting or node budget")
	}
	if node.Kind == yaml.AliasNode {
		return invalid("a document uses an unsupported YAML alias")
	}
	switch node.ShortTag() {
	case "!!map", "!!seq", "!!str", "!!bool", "!!null", "!!int", "!!float", "!!timestamp":
	default:
		return invalid("a document uses an unsupported YAML alias or tag")
	}
	if node.Kind == yaml.MappingNode {
		keys := make(map[string]*yaml.Node, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || keys[key.Value] != nil {
				return invalid("a mapping has a repeated or non-string key")
			}
			switch key.Value {
			case "$id", "$anchor", "$dynamicAnchor", "$dynamicRef", "$recursiveAnchor", "$recursiveRef":
				return invalid("a document uses an unsupported reference base or anchor")
			}
			keys[key.Value] = node.Content[i+1]
		}
		b.mapping[node] = keys
	}
	for _, child := range node.Content {
		if err := b.index(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// walk checks the graph without expanding it. Caching the longest ref path
// from each node also enforces the depth limit when that node is shared.
func (b *bundle) walk(file string, node *yaml.Node, hops int) (int, error) {
	if hops > maxReferenceDepth || b.active[node] {
		return 0, invalid("a reference is cyclic or exceeds the reference depth")
	}
	if height, ok := b.heights[node]; ok {
		if hops+height > maxReferenceDepth {
			return 0, invalid("a reference exceeds the reference depth")
		}
		return height, nil
	}
	b.active[node] = true
	defer delete(b.active, node)

	var height int
	if reference := b.mapping[node]["$ref"]; reference != nil {
		b.references++
		if b.references > maxReferences || !nonemptyString(reference) || len(reference.Value) > maxReferenceBytes {
			return 0, invalid("a reference is invalid or exceeds the reference budget")
		}
		name, target, err := b.target(file, reference.Value)
		if err != nil {
			return 0, err
		}
		if !b.used[name] {
			b.used[name] = true
			b.queue = append(b.queue, name)
		}
		targetHeight, err := b.walk(name, target, hops+1)
		if err != nil {
			return 0, err
		}
		height = targetHeight + 1
	}
	for _, child := range node.Content {
		childHeight, err := b.walk(file, child, hops)
		if err != nil {
			return 0, err
		}
		height = max(height, childHeight)
	}
	b.heights[node] = height
	return height, nil
}

func (b *bundle) target(file, value string) (string, *yaml.Node, error) {
	ref, err := url.Parse(value)
	if err != nil || ref.Scheme != "" || ref.Opaque != "" || ref.Host != "" ||
		ref.User != nil || ref.RawQuery != "" || ref.ForceQuery || path.IsAbs(ref.Path) {
		return "", nil, invalid("a reference leaves the uploaded bundle")
	}
	name := file
	if ref.Path != "" {
		name = path.Join(path.Dir(file), ref.Path)
	}
	target := b.documents[name]
	if !domain.ValidSourcePath(name) || target == nil {
		return "", nil, invalid("a reference file is outside or missing from the uploaded bundle")
	}
	if ref.Fragment == "" {
		return name, target, nil
	}
	pointer, ok := strings.CutPrefix(ref.Fragment, "/")
	if !ok || invalidPointerEscape.MatchString(pointer) {
		return "", nil, invalid("a reference fragment is not a JSON Pointer")
	}
	for _, segment := range strings.Split(pointer, "/") {
		key := unescapePointer.Replace(segment)
		switch target.Kind {
		case yaml.MappingNode:
			target = b.mapping[target][key]
		case yaml.SequenceNode:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(target.Content) || strconv.Itoa(index) != key {
				return "", nil, invalid("a reference does not resolve to a node")
			}
			target = target.Content[index]
		default:
			return "", nil, invalid("a reference does not resolve to a node")
		}
		if target == nil {
			return "", nil, invalid("a reference does not resolve to a node")
		}
	}
	return name, target, nil
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidSource, reason)
}
