package lima

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/saeta-eth/watermelon/internal/config"
)

//go:embed runtime/*
var runtimeAssets embed.FS

type runtimeAsset struct{ Name, Content string }
type runtimeCopy struct{ Image, Script string }

func prepareRuntimeAssets(cfg *config.Config, root string) ([]runtimeAsset, error) {
	projects := cfg.Setup.Projects
	if cfg.Setup.Profile != "multi" {
		projects = []config.SetupProject{{Directory: ".", Profile: cfg.Setup.Profile, Manager: cfg.Setup.Manager}}
	}
	data, err := json.Marshal(struct {
		Root     string                `json:"root"`
		Projects []config.SetupProject `json:"projects"`
		Corepack string                `json:"corepack"`
	}{root, projects, config.CorepackVersion})
	if err != nil {
		return nil, err
	}
	files := []runtimeAsset{{Name: "projects.json", Content: string(data)}}
	for _, name := range []string{"node-manager.cjs", "python-manager.py"} {
		data, err := runtimeAssets.ReadFile("runtime/" + name)
		if err != nil {
			return nil, err
		}
		files = append(files, runtimeAsset{Name: name, Content: string(data)})
	}
	for _, command := range config.AllSetupCommands(cfg.Setup) {
		var script string
		switch command {
		case "java":
			script = `#!/bin/sh
exec /usr/local/java/bin/java "-Duser.home=$HOME" "$@"
`
		case "pnpm", "yarn":
			script = "#!/bin/sh\nexec /usr/local/bin/node /watermelon-runtime/node-manager.cjs " + command + " \"$@\"\n"
		case "uv", "poetry":
			script = "#!/bin/sh\nexec /usr/local/bin/python -I /watermelon-runtime/python-manager.py " + command + " \"$@\"\n"
		}
		if script != "" {
			files = append(files, runtimeAsset{Name: "bin/" + command, Content: script})
		}
	}
	return files, nil
}

func configureExtendedRuntime(data *templateData, profiles []config.SetupConfig) error {
	for _, profile := range profiles {
		if config.ExtendedProfile(profile.Profile) {
			data.SetupExtendedAssembly = true
		}
	}
	if !data.SetupExtendedAssembly {
		return nil
	}
	base := "python:3.13-bookworm"
	for _, profile := range profiles {
		if profile.Profile == "python" {
			base, _ = config.SetupSourceImage(profile)
		}
	}
	data.SetupBaseImage = base
	// Replace local output tags with actual official donor images.
	var images []string
	for _, image := range data.BootstrapImages {
		if image != data.SetupImage && !strings.HasPrefix(image, "watermelon-project:") {
			images = append(images, image)
		}
	}
	images = append(images, base)
	data.SetupAssemblyCopies = nil
	for _, profile := range profiles {
		image, err := config.SetupSourceImage(profile)
		if err != nil {
			return err
		}
		images = append(images, image)
		script := "mkdir -p /watermelon-toolchains/usr/local; cp -a /usr/local/. /watermelon-toolchains/usr/local/"
		switch profile.Profile {
		case "java":
			data.SetupJava = true
			script = "mkdir -p /watermelon-toolchains/usr/local/java; cp -a /opt/java/openjdk/. /watermelon-toolchains/usr/local/java/"
			for _, extra := range []runtimeCopy{{"maven:3.9-eclipse-temurin-21", "mkdir -p /watermelon-toolchains/usr/local/share/maven; cp -a /usr/share/maven/. /watermelon-toolchains/usr/local/share/maven/"}, {"gradle:9.1-jdk21", "mkdir -p /watermelon-toolchains/usr/local/share/gradle; cp -a /opt/gradle/. /watermelon-toolchains/usr/local/share/gradle/"}} {
				images = append(images, extra.Image)
				data.SetupAssemblyCopies = append(data.SetupAssemblyCopies, extra)
			}
		case "dotnet":
			// Retain older supported shared runtimes for projects that target
			// net8/net9 while building with a newer SDK. SDK selection stays singular.
			major := strings.Split(profile.Runtime, ".")[0]
			older := []string{}
			if major == "10" || major == "" {
				older = []string{"8.0", "9.0"}
			} else if major == "9" {
				older = []string{"8.0"}
			}
			for _, version := range older {
				donor := "mcr.microsoft.com/dotnet/aspnet:" + version + "-bookworm-slim"
				images = append(images, donor)
				data.SetupAssemblyCopies = append(data.SetupAssemblyCopies, runtimeCopy{donor, "mkdir -p /watermelon-toolchains/usr/local/share/dotnet/shared; cp -a /usr/share/dotnet/shared/. /watermelon-toolchains/usr/local/share/dotnet/shared/"})
			}
			data.SetupDotnet = true
			script = "mkdir -p /watermelon-toolchains/usr/local/share/dotnet; cp -a /usr/share/dotnet/. /watermelon-toolchains/usr/local/share/dotnet/"
		case "ruby":
			data.SetupRuby = true
		case "php":
			data.SetupPHP = true
			data.SetupPHPImage = image
			images = append(images, "composer:2.8")
			data.SetupAssemblyCopies = append(data.SetupAssemblyCopies, runtimeCopy{"composer:2.8", "mkdir -p /watermelon-toolchains/usr/local/bin; cp /usr/bin/composer /watermelon-toolchains/usr/local/bin/composer; chmod 0755 /watermelon-toolchains/usr/local/bin/composer"})
		case "cpp":
			data.SetupCPP = true
		}
		if image != base {
			data.SetupAssemblyCopies = append(data.SetupAssemblyCopies, runtimeCopy{image, script})
		}
	}
	seen := map[string]bool{}
	data.BootstrapImages = nil
	for _, image := range images {
		if !seen[image] {
			seen[image] = true
			data.BootstrapImages = append(data.BootstrapImages, image)
		}
	}
	if len(data.SetupAssemblyCopies) == 0 {
		return fmt.Errorf("extended runtime has no toolchain sources")
	}
	return nil
}
