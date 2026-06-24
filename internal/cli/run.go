package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Enter the project sandbox VM",
		Long:  "Start the project VM (creating it if needed) and open an interactive shell.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun()
		},
	}
}

func runRun() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := loadProjectConfig(dir)
	if err != nil {
		return err
	}

	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Start verdict server for ask enforcement mode
	var verdictListener net.Listener
	var verdictPort int
	if cfg.Security.Enforcement == "ask" {
		if err := ensureNfqdBinary(dir); err != nil {
			return fmt.Errorf("building nfqd: %w", err)
		}

		// Try to read saved port (from previous VM creation)
		verdictPort = readSavedPort(dir)

		listenAddr := fmt.Sprintf("0.0.0.0:%d", verdictPort) // 0 if no saved port
		var listenErr error
		verdictListener, listenErr = net.Listen("tcp", listenAddr)
		if listenErr != nil {
			return fmt.Errorf("starting verdict server: %w", listenErr)
		}
		defer verdictListener.Close()

		verdictPort = verdictListener.Addr().(*net.TCPAddr).Port

		configPath := filepath.Join(dir, ".watermelon.toml")
		project := filepath.Base(dir)
		srv := ask.NewServer(project, configPath, ask.ShowDialog)
		go srv.Serve(verdictListener)
		fmt.Printf("Verdict server listening on port %d...\n", verdictPort)
	}

	vmName := lima.VMNameFromPath(dir)
	status := lima.GetStatus(vmName)

	if status == lima.StatusNotFound {
		fmt.Println("Creating sandbox VM...")

		// Save verdict port for future sessions
		if verdictPort > 0 {
			savePort(dir, verdictPort)
		}

		// Setup SSH config for IDE access
		if err := lima.EnsureSSHConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
		}

		yamlContent, err := lima.GenerateConfig(cfg, dir, verdictPort)
		if err != nil {
			return fmt.Errorf("generating Lima config: %w", err)
		}

		// Write temp Lima config
		tmpFile, err := os.CreateTemp("", "watermelon-*.yaml")
		if err != nil {
			return fmt.Errorf("creating temp config file: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString(yamlContent); err != nil {
			tmpFile.Close()
			return err
		}
		tmpFile.Close()

		if err := lima.Start(vmName, tmpFile.Name()); err != nil {
			return fmt.Errorf("starting VM: %w", err)
		}
	} else if status == lima.StatusStopped {
		fmt.Println("Starting sandbox VM...")
		if err := lima.Start(vmName, ""); err != nil {
			return fmt.Errorf("starting VM: %w", err)
		}
	}

	sshHost := lima.GetSSHHost(vmName)
	fmt.Printf("IDE: connect to %s\n", sshHost)
	fmt.Println()

	return lima.Shell(vmName)
}

func loadProjectConfig(dir string) (*config.Config, error) {
	configPath := filepath.Join(dir, ".watermelon.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no .watermelon.toml found (run 'watermelon init' first)")
	}
	return config.ParseFile(configPath)
}

func ensureNfqdBinary(projectDir string) error {
	binDir := filepath.Join(projectDir, ".watermelon", "bin")
	nfqdPath := filepath.Join(binDir, "watermelon-nfqd")

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}

	if source, err := findNfqdBinary(); err == nil {
		fmt.Println("Installing network interceptor for VM...")
		return copyExecutable(source, nfqdPath)
	}

	sourceRoot, err := findWatermelonSourceRoot()
	if err != nil {
		return errors.New("watermelon-nfqd sidecar not found; install the release sidecar or set WATERMELON_NFQD_BINARY")
	}

	fmt.Println("Building network interceptor for VM...")
	cmd := exec.Command("go", "build", "-o", nfqdPath, "./cmd/watermelon-nfqd")
	cmd.Dir = sourceRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func findNfqdBinary() (string, error) {
	if override := os.Getenv("WATERMELON_NFQD_BINARY"); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("WATERMELON_NFQD_BINARY %q does not exist", override)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{
		"watermelon-nfqd-linux-" + runtime.GOARCH,
		"watermelon-nfqd",
	} {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func findWatermelonSourceRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		nfqdMain := filepath.Join(dir, "cmd", "watermelon-nfqd", "main.go")
		if data, err := os.ReadFile(goMod); err == nil &&
			strings.Contains(string(data), "module github.com/saeta-eth/watermelon") {
			if _, err := os.Stat(nfqdMain); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

func copyExecutable(source, dest string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".watermelon-nfqd-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

func readSavedPort(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, ".watermelon", "verdict-port"))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return port
}

func savePort(dir string, port int) {
	portPath := filepath.Join(dir, ".watermelon", "verdict-port")
	os.MkdirAll(filepath.Dir(portPath), 0755)
	os.WriteFile(portPath, []byte(strconv.Itoa(port)), 0644)
}
