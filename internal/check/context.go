package check

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Person is an author or a codechecker.
type Person struct {
	Name string `yaml:"name" json:"name"`
	// ORCID is written "ORCID" in a codecheck.yml and "orcid" in a published
	// certificate's index.json.
	ORCID string `yaml:"ORCID" json:"orcid"`

	// path is where the person is in a codecheck.yml, "codechecker.0", so a
	// finding about them can say which line to look at. Empty for a person
	// read from anywhere else.
	path string
}

// ManifestItem is one output the workflow produces.
type ManifestItem struct {
	File    string `yaml:"file"`
	Comment string `yaml:"comment"`

	// path is where the item is in the file, "manifest.2", see Config.Line.
	path string
}

// Paper is the metadata about the checked article.
type Paper struct {
	Title     string   `yaml:"title"`
	Authors   []Person `yaml:"authors"`
	Reference string   `yaml:"reference"`

	// reference-other is new in specification 2.0. Whether it is present and
	// whether it is a sequence are two different rules, so both are recorded
	// rather than collapsed into an empty slice.
	ReferenceOther       []string
	HasReferenceOther    bool
	ReferenceOtherIsList bool
}

// Config is a parsed codecheck.yml.
//
// Presence and emptiness are different things to several rules - an absent
// manifest fails, an empty one does not - so the nodes whose absence a rule
// asks about carry a Has flag.
type Config struct {
	Version string
	// HasVersion is whether the file has a version node at all. An empty one
	// is a version nobody can apply, not a file that names none, see
	// SpecVersion.
	HasVersion  bool
	Manifest    []ManifestItem
	HasManifest bool
	Codechecker []Person
	Report      string
	Paper       Paper
	HasPaper    bool
	Summary     string
	Certificate string
	// CheckTime is the check_time node: when the CODECHECK was performed. It
	// dates a configuration that names no specification version.
	CheckTime  string
	Repository []string

	// Strings holds every string value in the file, for the rules that ask
	// about values wherever they appear.
	Strings []Scalar

	// lines maps where a node is, "paper.title" or "manifest.2.file", to the
	// line it starts on. See Line.
	lines map[string]int
}

// A Scalar is one value in the file and the line it is on.
type Scalar struct {
	Value string
	Line  int
}

// Line is the line a node starts on, or the line of the nearest node that
// contains it: a paper with no title is reported where the paper is, which is
// where the title has to be added. Zero when not even the top-level node is
// there, and a finding then carries no line rather than a wrong one.
//
// A path is the keys and the sequence positions from the root, joined with
// dots, positions counted from zero: "paper.authors.1.ORCID".
func (c Config) Line(path string) int {
	for path != "" {
		if line, ok := c.lines[path]; ok {
			return line
		}
		cut := strings.LastIndex(path, ".")
		if cut < 0 {
			break
		}
		path = path[:cut]
	}
	return 0
}

// Context is everything the checks need about the file under validation.
//
// Services is the outside world, and is nil unless external services are
// enabled; the checks that need it then skip rather than guess. See
// services.go.
type Context struct {
	Config     Config
	Raw        []byte
	Path       string
	ParseError error
	Label      string
	Services   *Services

	// Bundle is where the files that go with the configuration are: the
	// directory it was read from, or the repository it was fetched from. Nil
	// when there is neither, and then the rules about the bundle skip. See
	// bundle.go.
	Bundle Bundle
	// Modified is when the configuration was last changed at its source, when
	// that is known: the last commit touching the file, or the publication of
	// the record it came from. It dates a file that names no specification
	// version, see SpecVersion.
	Modified time.Time

	// RepositorySpec is set when the configuration was fetched from a
	// repository rather than read from disk, see source.go.
	RepositorySpec RepositorySpec
}

// WithServices returns the context with the outside world attached.
func (c Context) WithServices(services *Services) Context {
	c.Services = services
	return c
}

// FromFile reads a codecheck.yml from disk.
//
// A file that does not parse is not an error here: it is a failure of
// CC-CFG-001, reported like any other rule, and the checks that need the
// parsed content skip for want of it.
func FromFile(path string) (Context, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Context{}, fmt.Errorf("no such codecheck.yml: %w", err)
	}
	context := FromBytes(raw)
	context.Path = path
	context.Bundle = newLocalBundle(filepath.Dir(path))
	context.Label = path
	return context, nil
}

// FromBytes parses a codecheck.yml held in memory. The rules about the file on
// disk skip, because there is no file to look at.
func FromBytes(raw []byte) Context {
	context := Context{Raw: raw, Label: "the given configuration"}

	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		context.ParseError = err
		return context
	}
	root := documentRoot(&document)
	if root == nil {
		return context
	}

	var parsed struct {
		Version     string         `yaml:"version"`
		Manifest    []ManifestItem `yaml:"manifest"`
		Codechecker []Person       `yaml:"codechecker"`
		Report      string         `yaml:"report"`
		Summary     string         `yaml:"summary"`
		Certificate string         `yaml:"certificate"`
		CheckTime   string         `yaml:"check_time"`
		Paper       struct {
			Title     string   `yaml:"title"`
			Authors   []Person `yaml:"authors"`
			Reference string   `yaml:"reference"`
		} `yaml:"paper"`
	}
	if err := root.Decode(&parsed); err != nil {
		context.ParseError = err
		return context
	}

	config := Config{
		Version:     parsed.Version,
		HasVersion:  hasKey(root, "version"),
		Manifest:    parsed.Manifest,
		Codechecker: parsed.Codechecker,
		Report:      parsed.Report,
		Summary:     parsed.Summary,
		Certificate: parsed.Certificate,
		CheckTime:   parsed.CheckTime,
		Strings:     stringValues(root),
		lines:       map[string]int{},
	}
	nodeLines(root, "", config.lines)
	for i := range config.Manifest {
		config.Manifest[i].path = fmt.Sprintf("manifest.%d", i)
	}
	for i := range config.Codechecker {
		config.Codechecker[i].path = fmt.Sprintf("codechecker.%d", i)
	}
	config.HasManifest = hasKey(root, "manifest")
	config.Repository = scalars(childNode(root, "repository"))
	config.HasPaper = hasKey(root, "paper")
	config.Paper = Paper{
		Title:     parsed.Paper.Title,
		Authors:   parsed.Paper.Authors,
		Reference: parsed.Paper.Reference,
	}
	for i := range config.Paper.Authors {
		config.Paper.Authors[i].path = fmt.Sprintf("paper.authors.%d", i)
	}

	if other := childNode(childNode(root, "paper"), "reference-other"); other != nil {
		config.Paper.HasReferenceOther = true
		if other.Kind == yaml.SequenceNode {
			config.Paper.ReferenceOtherIsList = true
			for _, entry := range other.Content {
				config.Paper.ReferenceOther = append(config.Paper.ReferenceOther, entry.Value)
			}
		}
	}

	context.Config = config
	return context
}

// SpecVersionFromURL returns the specification version a **published** version
// URL names, or "" if it names none. A trailing slash is tolerated, as is
// http. This is what CC-CFG-015 asks about.
func SpecVersionFromURL(version string) string {
	return specVersionNamed(version, `spec/config/`)
}

// SpecVersionNamed returns the version a version node names, including the
// historical form https://codecheck.org.uk/spec/1.0, which certificates from
// 2020 use and which was never redirected to the published page.
//
// It decides only which rules to apply. CC-CFG-015 still reports the
// historical URL as unpublished, because a reader following it gets a 404.
// See "Choosing the specification version" in the register's RULES.md.
func SpecVersionNamed(version string) string {
	if published := SpecVersionFromURL(version); published != "" {
		return published
	}
	return specVersionNamed(version, `spec/`)
}

func specVersionNamed(version, prefix string) string {
	for _, candidate := range specVersions {
		pattern := regexp.MustCompile(prefix + regexp.QuoteMeta(candidate) + `/?$`)
		if pattern.MatchString(strings.TrimSpace(version)) {
			return candidate
		}
	}
	return ""
}

// specVersions is set from the rules package by the runner, so that the list
// of known versions lives in one place.
var specVersions []string

func documentRoot(document *yaml.Node) *yaml.Node {
	if document.Kind == yaml.DocumentNode && len(document.Content) > 0 {
		return document.Content[0]
	}
	if document.Kind == yaml.MappingNode {
		return document
	}
	return nil
}

func hasKey(mapping *yaml.Node, key string) bool {
	return childNode(mapping, key) != nil
}

func childNode(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// scalars returns a node's values, whether it is written as one value or as a
// sequence: repository accepts both.
func scalars(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(node.Value) == "" {
			return nil
		}
		return []string{node.Value}
	case yaml.SequenceNode:
		var values []string
		for _, child := range node.Content {
			values = append(values, scalars(child)...)
		}
		return values
	}
	return nil
}

// nodeLines records the line of every node below one, by path, see
// Config.Line. A key's line is the key's, not its value's: a block sequence
// starts on the line after its key, and the key is what a reader looks for.
func nodeLines(node *yaml.Node, path string, lines map[string]int) {
	below := func(step string) string {
		if path == "" {
			return step
		}
		return path + "." + step
	}
	switch node.Kind {
	case yaml.SequenceNode:
		for i, child := range node.Content {
			item := below(fmt.Sprint(i))
			lines[item] = child.Line
			nodeLines(child, item, lines)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := below(node.Content[i].Value)
			lines[key] = node.Content[i].Line
			nodeLines(node.Content[i+1], key, lines)
		}
	}
}

// stringValues collects every scalar in the document, keys excluded.
func stringValues(node *yaml.Node) []Scalar {
	var values []Scalar
	switch node.Kind {
	case yaml.ScalarNode:
		values = append(values, Scalar{Value: node.Value, Line: node.Line})
	case yaml.SequenceNode:
		for _, child := range node.Content {
			values = append(values, stringValues(child)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			values = append(values, stringValues(node.Content[i+1])...)
		}
	}
	return values
}
