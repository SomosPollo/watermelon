package lima

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

type VMInfo struct {
	Name   string
	Status string
	Dir    string
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
				Name:   vm.Name,
				Status: vm.Status,
				Dir:    vm.Dir,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
