package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/saeta-eth/watermelon/internal/config"
	"github.com/saeta-eth/watermelon/internal/project"
	"github.com/spf13/cobra"
)

type setupReview struct {
	config   config.Config
	plan     project.Plan
	document []byte
	deferred bool
}

type setupPrompt struct {
	cmd    *cobra.Command
	reader *bufio.Reader
}

func (p *setupPrompt) line(label string) (string, error) {
	if err := p.cmd.Context().Err(); err != nil {
		return "", err
	}
	fmt.Fprint(p.cmd.OutOrStdout(), label)
	type answer struct {
		text string
		err  error
	}
	input := make(chan answer, 1)
	go func() {
		line, err := p.reader.ReadString('\n')
		input <- answer{strings.TrimSpace(line), err}
	}()
	select {
	case <-p.cmd.Context().Done():
		return "", p.cmd.Context().Err()
	case result := <-input:
		if result.err != nil && (result.err != io.EOF || result.text == "") {
			return "", result.err
		}
		return result.text, nil
	}
}

// Review a draft rather than modifying the policy of the acquisition VM.
// Only the final, displayed plan can be approved and installed.
func reviewCloneSetup(cmd *cobra.Command, root string, original []byte, files map[string][]byte, cfg config.Config, plan project.Plan) (setupReview, error) {
	if err := requireSetupTerminal(cmd); err != nil {
		return setupReview{}, err
	}
	document, err := config.UpdateDocument(original, &cfg)
	if err != nil {
		return setupReview{}, err
	}
	review := setupReview{config: cfg, plan: plan, document: document}
	before, err := config.Parse(original)
	if err != nil {
		return setupReview{}, err
	}
	prompt := setupPrompt{cmd: cmd, reader: bufio.NewReader(cmd.InOrStdin())}
	for {
		printSetupPlan(cmd, &review.config, review.plan)
		beforeDigest, _ := config.AppliedConfigDigest(before)
		afterDigest, _ := config.AppliedConfigDigest(&review.config)
		if before.Setup.Profile != "" && beforeDigest != afterDigest {
			fmt.Fprintln(cmd.OutOrStdout(), "This plan replaces the VM while retaining managed workspace data and mounted project files.")
		}
		choice, err := prompt.line("Prepare this project? [y/N, c=customize, e=edit TOML, s=setup later] ")
		if err != nil {
			return setupReview{}, fmt.Errorf("setup was not approved; source files were retained: %w", err)
		}
		switch strings.ToLower(choice) {
		case "y", "yes", "continue":
			return review, nil
		case "s", "later":
			review.deferred = true
			fmt.Fprintln(cmd.OutOrStdout(), "Setup deferred. Draft customizations were not saved. Run 'watermelon up' to review setup again.")
			return review, nil
		case "", "n", "no", "q", "quit":
			return setupReview{}, fmt.Errorf("setup was not approved; source files were retained")
		case "c", "customize":
			updated, updatedPlan, err := customizeSetup(&prompt, &review.config, review.plan, files)
			if err != nil {
				if cmd.Context().Err() != nil || err == io.EOF {
					return setupReview{}, err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Customization was not applied: %v\n", err)
				continue
			}
			document, err := config.UpdateDocument(review.document, &updated)
			if err != nil {
				return setupReview{}, err
			}
			review.config, review.plan, review.document = updated, updatedPlan, document
		case "e", "edit":
			document, err := editConfigDocument(cmd, root, review.document)
			if err != nil {
				if cmd.Context().Err() != nil {
					return setupReview{}, err
				}
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				continue
			}
			updated, err := config.Parse(document)
			if err != nil {
				return setupReview{}, err
			}
			if updated.Security.Enforcement == config.EnforcementAsk {
				fmt.Fprintln(cmd.ErrOrStderr(), "Automatic setup requires fail, silent, or log enforcement; use a foreground run session for ask mode.")
				continue
			}
			updatedPlan, err := project.DetectConfigured(files, updated.Setup)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Cannot prepare edited setup: %v\n", err)
				continue
			}
			proposed := proposeProfile(updated, updatedPlan)
			if err := config.Validate(&proposed); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				continue
			}
			document, err = config.UpdateDocument(document, &proposed)
			if err != nil {
				return setupReview{}, err
			}
			review.config, review.plan, review.document = proposed, updatedPlan, document
		default:
			fmt.Fprintln(cmd.ErrOrStderr(), "Choose y, n, c, e, or s.")
		}
	}
}

func customizeSetup(prompt *setupPrompt, cfg *config.Config, plan project.Plan, files map[string][]byte) (config.Config, project.Plan, error) {
	// Decode a copy so rejected edits cannot mutate the reviewed slices/maps.
	var encoded strings.Builder
	if err := toml.NewEncoder(&encoded).Encode(cfg); err != nil {
		return config.Config{}, plan, err
	}
	copy, err := config.Parse([]byte(encoded.String()))
	if err != nil {
		return config.Config{}, plan, err
	}
	fmt.Fprintln(prompt.cmd.OutOrStdout(), "Press Enter to keep each setting. Use '-' to clear forwarded ports.")
	runtime, err := prompt.line("Runtime override (e.g. node=22,python=3.13): ")
	if err != nil {
		return config.Config{}, plan, err
	}
	if runtime != "" {
		plan, err = project.DetectConfiguredWithRuntime(files, copy.Setup, runtime)
		if err != nil {
			return config.Config{}, plan, err
		}
		updated := proposeProfile(copy, plan)
		copy = &updated
	}
	for _, field := range []struct {
		label string
		value *string
	}{
		{"Memory", &copy.Resources.Memory},
		{"VM system disk", &copy.Resources.Disk},
	} {
		answer, err := prompt.line(fmt.Sprintf("%s [%s]: ", field.label, *field.value))
		if err != nil {
			return config.Config{}, plan, err
		}
		if answer != "" {
			if err := config.ValidateResourceSize(answer); err != nil {
				return config.Config{}, plan, err
			}
			*field.value = answer
		}
	}
	answer, err := prompt.line(fmt.Sprintf("CPUs [%d]: ", copy.Resources.CPUs))
	if err != nil {
		return config.Config{}, plan, err
	}
	if answer != "" {
		copy.Resources.CPUs, err = strconv.Atoi(answer)
		if err != nil {
			return config.Config{}, plan, fmt.Errorf("CPUs must be a positive integer")
		}
	}
	answer, err = prompt.line(fmt.Sprintf("Forwarded localhost ports %v (comma-separated): ", copy.Ports.Forward))
	if err != nil {
		return config.Config{}, plan, err
	}
	if answer != "" {
		copy.Ports.Forward = nil
		if answer != "-" {
			for _, field := range strings.Split(answer, ",") {
				port, err := strconv.Atoi(strings.TrimSpace(field))
				if err != nil {
					return config.Config{}, plan, fmt.Errorf("forwarded ports must be comma-separated integers")
				}
				copy.Ports.Forward = append(copy.Ports.Forward, port)
			}
		}
	}
	answer, err = prompt.line("Additional allowed destinations (comma-separated hosts or host:port): ")
	if err != nil {
		return config.Config{}, plan, err
	}
	if answer != "" {
		for _, field := range strings.Split(answer, ",") {
			copy.Network.Allow = append(copy.Network.Allow, strings.TrimSpace(field))
		}
	}
	if err := config.Validate(copy); err != nil {
		return config.Config{}, plan, err
	}
	return *copy, plan, nil
}
