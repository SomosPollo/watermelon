package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saeta-eth/watermelon/internal/ask"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	var name, workdir string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Enter the project sandbox VM",
		Long:  "Start the project VM (creating it if needed) and open an interactive shell.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(name, workdir)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides path-derived name and vm.name config)")
	cmd.Flags().StringVar(&workdir, "workdir", "", "Working directory inside VM (overrides config)")
	return cmd
}

func runRun(name, workdir string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	configIsDefault := false
	cfg, err := loadProjectConfig(dir)
	if err != nil {
		if name != "" {
			cfg = config.NewConfig()
			configIsDefault = true
		} else {
			return err
		}
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

	vmName := resolveVMNameFromConfig(name, cfg.VM.Name, dir)
	status := lima.GetStatus(vmName)

	if status == lima.StatusNotFound {
		fmt.Println("Creating sandbox VM...")

		if verdictPort > 0 {
			savePort(dir, verdictPort)
		}

		if err := lima.EnsureSSHConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not configure SSH: %v\n", err)
		}

		yamlContent, err := lima.GenerateConfig(cfg, dir, verdictPort)
		if err != nil {
			return fmt.Errorf("generating Lima config: %w", err)
		}

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

	effectiveWorkdir := workdir
	if effectiveWorkdir == "" && !configIsDefault {
		effectiveWorkdir = cfg.IDE.Workdir
		if effectiveWorkdir == "" {
			effectiveWorkdir = config.DefaultWorkdir(cfg)
		}
	}

	return lima.Shell(vmName, effectiveWorkdir)
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

	fmt.Println("Building network interceptor for VM...")
	cmd := exec.Command("go", "build", "-o", nfqdPath, "./cmd/watermelon-nfqd")
	cmd.Env = append(os.Environ(), "GOOS=linux")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
