package cli

import (
	"strings"
	"testing"
)

func TestValidateCopyArgs(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		dst     string
		wantErr bool
		errMsg  string
	}{
		{"host to vm", "./file.txt", "myvm:/tmp/", false, ""},
		{"vm to host", "myvm:/tmp/out.log", "./", false, ""},
		{"absolute host to vm", "/home/user/file.txt", "myvm:/tmp/", false, ""},
		{"both vm paths", "vm1:/a", "vm2:/b", true, "both src and dst"},
		{"neither vm path", "./src", "./dst", true, "neither src nor dst"},
		{"vm with nested path", "somospollo-vm:/home/user/SomosPollo/", "./backup/", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCopyArgs(tt.src, tt.dst)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCopyArgs(%q, %q) error = %v, wantErr %v", tt.src, tt.dst, err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}
