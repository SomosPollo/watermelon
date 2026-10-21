package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/saeta-eth/watermelon/internal/lima"
	"github.com/saeta-eth/watermelon/internal/logs"
	"github.com/spf13/cobra"
)

func NewLogsCmd() *cobra.Command {
	var clear bool
	var name string

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show network policy logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			target, err := resolveManagementTarget(dir, name)
			if err != nil {
				return err
			}
			lifecycleLock, err := acquireVMLifecycleLock(target.VMName)
			if err != nil {
				return fmt.Errorf("locking VM %q lifecycle: %w", target.VMName, err)
			}
			defer lifecycleLock.Release()
			if target.Config != nil && target.Config.Workspace.Disk != "" {
				return managedNetworkLogs(cmd, target, clear)
			}
			logPath, err := resolvedNetworkLogPath(target)
			if err != nil {
				return err
			}

			if clear {
				return logs.ClearPath(logPath)
			}

			lines, err := logs.ReadPath(logPath)
			if err != nil {
				return err
			}

			if len(lines) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No logs recorded")
				return nil
			}

			for _, line := range lines {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&clear, "clear", false, "Clear the log")
	cmd.Flags().StringVar(&name, "name", "", "VM name (overrides vm.name and the name derived from the resolved project root)")
	return cmd
}

var managedLogOutput = lima.GuestOutput

func managedNetworkLogs(cmd *cobra.Command, target targetContext, clear bool) error {
	if cliGetVMStatus(target.VMName) != lima.StatusRunning {
		return fmt.Errorf("managed logs are retained on the workspace disk; start the sandbox with 'watermelon run --no-shell' to read or clear them")
	}
	if err := requireVMProjectBinding(target.ProjectRoot, target.VMName); err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	mode := "read"
	if clear {
		mode = "clear"
	}
	args := []string{"/usr/bin/python3", "-I", "-c", managedLogReader, "/mnt/lima-" + target.Config.Workspace.Disk + "/logs/logs.log", mode}
	output, err := managedLogOutput(ctx, target.VMName, "/", args, 4<<20)
	if err != nil {
		return err
	}
	if clear {
		return nil
	}
	if len(output) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No logs recorded")
		return nil
	}
	_, err = cmd.OutOrStdout().Write(output)
	return err
}

// Read only a bounded regular file through no-follow descriptors. The path is
// derived from host-owned disk metadata, never supplied by repository code.
const managedLogReader = `import os,stat,sys
directory=os.open('/',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
try:
 for component in os.path.dirname(sys.argv[1]).split('/'):
  if not component: continue
  child=os.open(component,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=directory)
  os.close(directory)
  directory=child
 flags=os.O_WRONLY if sys.argv[2]=='clear' else os.O_RDONLY
 try: fd=os.open(os.path.basename(sys.argv[1]),flags|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=directory)
 except FileNotFoundError: sys.exit(0)
 try:
  info=os.fstat(fd)
  if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_uid!=os.getuid(): raise ValueError('unsafe guest log file')
  if sys.argv[2]=='clear': os.ftruncate(fd,0)
  else:
   start=max(0,info.st_size-4194304)
   os.lseek(fd,start,os.SEEK_SET)
   with os.fdopen(fd,'rb',closefd=False) as stream: data=stream.read(4194304)
   if start:
    offset=data.find(b'\n')
    data=data[offset+1:] if offset>=0 else b''
   sys.stdout.buffer.write(data)
 finally: os.close(fd)
finally: os.close(directory)`

func resolvedNetworkLogPath(target targetContext) (string, error) {
	logPath := logs.LogPath(target.ProjectRoot)
	instance, err := loadOwnedNamedVMIdentity(target.ProjectRoot, target.VMName)
	if err == nil {
		return instance.Paths.GuestNetworkLogPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolving logs for VM %q: %w", target.VMName, err)
	}
	if target.VMName != derivedVMName(target.ProjectRoot) {
		return "", fmt.Errorf("refusing to read logs for custom-named VM %q without a valid Watermelon identity record", target.VMName)
	}
	return logPath, nil
}
