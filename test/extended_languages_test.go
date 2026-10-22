//go:build e2e

package test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2EExtendedLanguages(t *testing.T) {
	requireNonShortRealVM(t)
	timings := realVMTimings(t)
	boot := 25 * time.Minute
	h := newHarness(t)
	resume := os.Getenv("WATERMELON_E2E_EXTENDED_RESUME")
	if resume != "" {
		canonical, err := filepath.EvalSymlinks(resume)
		if err != nil || !strings.HasPrefix(filepath.Base(canonical), "wm-e2e-") || (filepath.Dir(canonical) != "/private/tmp" && filepath.Dir(canonical) != "/tmp") {
			t.Fatal("resume requires a disposable E2E root")
		}
		h.root = resume
		h.home = filepath.Join(resume, "home")
		h.env = append(h.env, "HOME="+h.home, "LIMA_HOME="+filepath.Join(h.home, ".lima"), "XDG_CONFIG_HOME="+filepath.Join(h.home, ".config"))
	}
	destination := filepath.Join(h.root, "extended")
	t.Cleanup(func() {
		h.project = destination
		if t.Failed() && os.Getenv("WATERMELON_E2E_KEEP_FAILED") == "1" {
			t.Logf("retained failed workspace: %s", h.root)
			return
		}
		h.destroyVM(timings.cleanup)
		digest := sha256.Sum256([]byte(destination))
		command := exec.Command("limactl", "disk", "delete", fmt.Sprintf("w%x", digest[:5]))
		command.Env = h.env
		if out, err := command.CombinedOutput(); err != nil {
			t.Logf("disk cleanup: %v %s", err, out)
		}
		if resume != "" {
			if err := os.RemoveAll(resume); err != nil {
				t.Error(err)
			}
		}
	})
	if resume == "" {
		seedManagedWorkspace(t, h, destination)
		h.project = destination
		h.run(timings.boot, "run", "--no-shell")
	}
	h.project = destination
	fixtures := map[string]string{
		"jvm/pom.xml":                    `<project><modelVersion>4.0.0</modelVersion><groupId>example</groupId><artifactId>fixture</artifactId><version>1.0</version><properties><maven.compiler.release>21</maven.compiler.release></properties><dependencies><dependency><groupId>org.apache.commons</groupId><artifactId>commons-lang3</artifactId><version>3.17.0</version></dependency></dependencies><build><plugins><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>3.13.0</version></plugin></plugins></build></project>`,
		"jvm/src/main/java/Main.java":    `import org.apache.commons.lang3.StringUtils; public class Main { public static void main(String[] args) { if (!StringUtils.capitalize("ready").equals("Ready")) throw new Error(); System.out.println("java-ready"); } }`,
		"kotlin/settings.gradle.kts":     `rootProject.name = "fixture"`,
		"kotlin/build.gradle.kts":        "plugins { kotlin(\"jvm\") version \"2.3.20\" }\nrepositories { mavenCentral() }\nkotlin { jvmToolchain(21) }\n",
		"kotlin/src/main/kotlin/Main.kt": "fun main() { println(\"kotlin-ready\") }\n",
		"dotnet/Fixture.csproj":          `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Humanizer.Core" Version="2.14.1" /></ItemGroup></Project>`,
		"dotnet/Program.cs":              `using System; using Humanizer; if ("some_name".Humanize() != "some name") throw new Exception(); Console.WriteLine("dotnet-ready");`,
		"dotnet8/Fixture.csproj":         `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
		"dotnet8/Program.cs":             `System.Console.WriteLine("dotnet8-ready");`,
		"ruby/Gemfile":                   "source 'https://rubygems.org'\ngem 'colorize', '=1.1.0'\n",
		"php/composer.json":              `{"require":{"php":"^8.2","psr/log":"3.0.2","ext-intl":"*"}}`,
		"cpp/CMakeLists.txt":             "cmake_minimum_required(VERSION 3.20)\nproject(fixture LANGUAGES C CXX)\nset(CMAKE_CXX_STANDARD 20)\nadd_executable(fixture main.cpp)\n",
		"cpp/main.cpp":                   "#include <iostream>\nint main(){std::cout << \"cpp-ready\\n\";}\n",
		"pnpm/package.json":              `{"name":"pnpm-fixture","version":"1.0.0","packageManager":"pnpm@10.20.0","scripts":{"postinstall":"node setup.cjs"},"dependencies":{"is-number":"7.0.0"}}`,
		"pnpm/setup.cjs":                 `for(const [cmd,args] of [['javac',['--version']],['dotnet',['--version']],['ruby',['-e','puts RUBY_VERSION']],['php',['-r','echo PHP_VERSION;']],['gcc',['-dumpfullversion']],['python',['--version']]]){const r=require('child_process').spawnSync(cmd,args,{stdio:'inherit'});if(r.status!==0)throw Error(cmd+' failed: '+r.error);}require('fs').writeFileSync('installed.txt','ready');`,
		// Yarn 4.18.1 fixes the network retry cancellation crash in older pins:
		// https://github.com/yarnpkg/berry/pull/7249
		"yarn/package.json":     `{"name":"yarn-fixture","version":"1.0.0","packageManager":"yarn@4.18.1","dependencies":{"is-number":"7.0.0"}}`,
		"uv/pyproject.toml":     "[project]\nname='uv-fixture'\nversion='0.1.0'\nrequires-python='>=3.12'\ndependencies=['idna==3.10']\n[tool.uv]\npackage=false\n",
		"poetry/pyproject.toml": "[project]\nname='poetry-fixture'\nversion='0.1.0'\nrequires-python='>=3.12,<4.0'\ndependencies=['idna==3.10']\n[tool.poetry]\npackage-mode=false\n",
		"edit.txt":              "retained-edit\n",
	}
	// Every write and all package/build execution occur in the guest.
	if resume == "" {
		for name, body := range fixtures {
			h.run(timings.command, "exec", "sh", "-c", `mkdir -p -- "$(dirname -- "$1")"; printf '%s' "$2" > "$1"`, "fixture", name, body)
		}
	}
	out := ""
	if resume != "" {
		out = h.run(boot, "rebuild", "--force", "--yes")
	} else {
		out = h.run(boot, "up", "--yes")
	}
	for _, language := range []string{"java 21", "dotnet 10.0", "ruby 3.4", "php 8.4", "cpp 12", "node 24", "python 3.13"} {
		if resume == "" && !strings.Contains(out, language) {
			t.Fatalf("missing %s plan: %s", language, out)
		}
	}
	checks := [][]string{
		{"exec", "--cwd", "jvm", "mvn", "-B", "-ntp", "--offline", "compile"},
		{"exec", "--cwd", "kotlin", "gradle", "--offline", "--no-daemon", "classes"},
		{"exec", "--cwd", "dotnet", "dotnet", "run", "--no-restore"},
		{"exec", "--cwd", "dotnet8", "dotnet", "run", "--no-restore"},
		{"exec", "--cwd", "ruby", "bundle", "exec", "ruby", "-r", "colorize", "-e", "puts 'ruby-ready'.green"},
		{"exec", "--cwd", "php", "php", "-r", `require 'vendor/autoload.php'; if(!interface_exists('Psr\Log\LoggerInterface') || !extension_loaded('intl')) exit(1); echo 'php-ready';`},
		{"exec", "--cwd", "cpp", "cmake", "--build", ".watermelon-build"},
		{"exec", "--cwd", "cpp", "./.watermelon-build/fixture"},
		{"exec", "--cwd", "pnpm", "pnpm", "exec", "node", "-e", `if(!require('is-number')(42)||!require('fs').existsSync('installed.txt'))process.exit(1)`},
		{"exec", "--cwd", "yarn", "yarn", "node", "-e", `if(!require('is-number')(42))process.exit(1)`},
		{"exec", "--cwd", "uv", "python", "-c", `import idna; assert idna.__version__=='3.10'`},
		{"exec", "--cwd", "poetry", "python", "-c", `import idna; assert idna.__version__=='3.10'`},
	}
	for _, args := range checks {
		h.run(boot, args...)
	}
	if out := h.run(timings.command, "up", "--yes"); !strings.Contains(out, "unchanged") {
		t.Fatalf("setup repeated: %s", out)
	}
	h.runBlockedNetwork(timings.command, "exec", "bash", "-lc", blockedNetworkProbe)
	h.run(timings.stop, "stop")
	h.run(boot, "exec", "--cwd", "pnpm", "node", "setup.cjs")
	// A recovery run has already rebuilt from retained fixture data at entry.
	// Fresh CI runs exercise rebuild after preparation, then repeat every check.
	if resume == "" {
		h.run(boot, "rebuild", "--force", "--yes")
		for _, args := range checks {
			h.run(boot, args...)
		}
	}
	if got := managedCommandStdout(t, h, timings.command, "exec", "cat", "edit.txt"); strings.TrimSpace(got) != "retained-edit" {
		t.Fatalf("edit lost: %q", got)
	}
}
