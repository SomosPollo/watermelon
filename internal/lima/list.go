package lima

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const vmListFormat = "{{json .Name}}\t{{json .Status}}\t{{json .Dir}}"

type VMInfo struct {
	Name       string
	Status     string
	Dir        string
	ProjectDir string
}

// ListAllVMs returns every Lima instance. Callers that own an independent
// Watermelon registry can use it to recognize custom names that do not carry
// the historical watermelon- prefix.
func ListAllVMs() ([]VMInfo, error) {
	cmd := execCommand("limactl", "list", "--format", vmListFormat)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// limactl returns empty output when no VMs exist
	if len(out) == 0 {
		return nil, nil
	}

	// Keep the Lima response narrow: full instance JSON embeds provisioning
	// scripts and can exceed limactl's internal output-scanner limit.
	records, err := parseLimaTemplateRecords(out, 3)
	if err != nil {
		return nil, fmt.Errorf("decoding Lima instance list: %w", err)
	}

	var result []VMInfo
	for _, record := range records {
		var name, status, dir string
		for fieldIndex, destination := range []*string{&name, &status, &dir} {
			if err := json.Unmarshal(record[fieldIndex], destination); err != nil {
				return nil, fmt.Errorf("decoding Lima instance list field %d: %w", fieldIndex+1, err)
			}
		}

		projectDir := ""
		// Only historical Watermelon names use /project as their ownership
		// signal. Do not inspect arbitrary unrelated Lima instance directories;
		// registered custom names are attributed by the CLI's identity registry.
		if strings.HasPrefix(name, "watermelon-") {
			projectDir = projectDirFromInstanceDir(dir)
		}
		result = append(result, VMInfo{
			Name:       name,
			Status:     status,
			Dir:        dir,
			ProjectDir: projectDir,
		})
	}

	return result, nil
}

// ListWatermelonVMs retains the historical prefix-based view. New code that
// has registry ownership information should filter ListAllVMs by that registry.
func ListWatermelonVMs() ([]VMInfo, error) {
	all, err := ListAllVMs()
	if err != nil {
		return nil, err
	}
	result := make([]VMInfo, 0, len(all))
	for _, vm := range all {
		if strings.HasPrefix(vm.Name, "watermelon-") {
			result = append(result, vm)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func projectDirFromInstanceDir(instanceDir string) string {
	for _, name := range []string{"lima.yaml", "lima.yml"} {
		data, err := os.ReadFile(filepath.Join(instanceDir, name))
		if err == nil {
			return parseProjectDirFromLimaConfig(string(data))
		}
	}
	return ""
}

func parseProjectDirFromLimaConfig(data string) string {
	scanner := bufio.NewScanner(strings.NewReader(data))
	location := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if strings.HasPrefix(line, "location:") {
			location = parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "location:")))
			continue
		}
		if strings.HasPrefix(line, "mountPoint:") {
			mountPoint := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "mountPoint:")))
			if mountPoint == "/project" {
				return location
			}
		}
	}
	return ""
}

func parseYAMLScalar(value string) string {
	if value == "" {
		return ""
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return strings.Trim(value, `"'`)
}
