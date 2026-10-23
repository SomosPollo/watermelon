package skills

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBundledSkillHasDiscoverableMetadata(t *testing.T) {
	parts := strings.SplitN(Watermelon, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatal("bundled skill is missing YAML frontmatter")
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "watermelon" || strings.TrimSpace(metadata.Description) == "" {
		t.Fatalf("skill cannot be discovered as watermelon: %+v", metadata)
	}
	if strings.TrimSpace(parts[2]) == "" {
		t.Fatal("bundled skill has no instructions")
	}
}

func TestBundledSkillLinksWorkOutsideSourceCheckout(t *testing.T) {
	// agents install distributes only SKILL.md. Links into the source checkout
	// disappear in the user's skill directory, so documentation links must be
	// absolute (or anchors within the skill itself).
	links := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`).FindAllStringSubmatch(Watermelon, -1)
	for _, link := range links {
		target := link[1]
		if strings.HasPrefix(target, "#") {
			continue
		}
		parsed, err := url.Parse(target)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			t.Errorf("bundled skill link %q depends on files not installed with the skill", target)
		}
	}
}
