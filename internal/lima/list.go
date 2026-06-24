package lima

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type VMInfo struct {
	Name       string
	Status     string
	Dir        string
	ProjectDir string
}

// ListWatermelonVMs returns all VMs created by watermelon
func ListWatermelonVMs() ([]VMInfo, error) {
	cmd := execCommand("limactl", "list", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// limactl returns empty output when no VMs exist
	if len(out) == 0 {
		return nil, nil
	}

	// limactl outputs newline-delimited JSON (one object per line)
	var result []VMInfo
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var vm struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Dir    string `json:"dir"`
		}

		if err := json.Unmarshal([]byte(line), &vm); err != nil {
			return nil, err
		}

		if strings.HasPrefix(vm.Name, "watermelon-") {
			result = append(result, VMInfo{
				Name:       vm.Name,
				Status:     vm.Status,
				Dir:        vm.Dir,
				ProjectDir: projectDirFromInstanceDir(vm.Dir),
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
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
