package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtendedLanguageDetection(t *testing.T) {
	for _, tt := range []struct{ profile, manifest, body, runtime, command string }{
		{"java", "pom.xml", "<project><properties><maven.compiler.release>17</maven.compiler.release></properties></project>", "21", "mvn"},
		{"java", "build.gradle.kts", `plugins { kotlin("jvm") version "2.3.20" }`, "21", "gradle"},
		{"dotnet", "Service.csproj", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`, "10.0", "dotnet"},
		{"ruby", "Gemfile", "source 'https://rubygems.org'\nruby '>=3.3'\n", "3.4", "bundle"},
		{"php", "composer.json", `{"require":{"php":"^8.3"}}`, "8.4", "composer"},
		{"cpp", "CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(example)", "12", "cmake"},
	} {
		t.Run(tt.profile+tt.manifest, func(t *testing.T) {
			p, err := Detect(map[string][]byte{tt.manifest: []byte(tt.body)}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if p.Profile != tt.profile || p.Runtime != tt.runtime || p.Commands[0][0] != tt.command {
				t.Fatalf("plan=%+v", p)
			}
			if !strings.HasPrefix(p.Image, "watermelon-project:") {
				t.Fatal("extended runtime bypasses trusted assembly")
			}
		})
	}
}

func TestPackageManagerSelectionAndLockfiles(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		files                  map[string][]byte
		manager, command, flag string
	}{
		{"pnpm", map[string][]byte{"package.json": []byte(`{"packageManager":"pnpm@10.20.0"}`), "pnpm-lock.yaml": nil}, "pnpm@10.20.0", "pnpm", "--frozen-lockfile"},
		{"yarn classic", map[string][]byte{"package.json": []byte(`{"packageManager":"yarn@1.22.22"}`), "yarn.lock": nil}, "yarn@1.22.22", "yarn", "--frozen-lockfile"},
		{"yarn modern", map[string][]byte{"package.json": []byte(`{"packageManager":"yarn@4.10.3"}`), "yarn.lock": nil}, "yarn@4.10.3", "yarn", "--immutable"},
		{"uv", map[string][]byte{"pyproject.toml": []byte("[project]\nrequires-python='>=3.12'\n[tool.uv]\nrequired-version='==0.12.0'"), "uv.lock": nil}, "uv@0.12.0", "uv", "--locked"},
		{"poetry", map[string][]byte{"pyproject.toml": []byte("[tool.poetry.dependencies]\npython='>=3.12,<3.13'"), "poetry.lock": nil}, "poetry@2.5.1", "poetry", "--no-interaction"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Detect(tt.files, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if p.Manager != tt.manager || p.Setup().Manager != tt.manager {
				t.Fatalf("manager selection lost: %+v", p)
			}
			commands := ""
			for _, args := range p.Commands {
				commands += strings.Join(args, " ")
			}
			if !strings.Contains(commands, tt.command) || !strings.Contains(commands, tt.flag) {
				t.Fatalf("lock not respected: %s", commands)
			}
			again, err := DetectConfigured(tt.files, p.Setup())
			if err != nil || again.Fingerprint != p.Fingerprint {
				t.Fatalf("unstable manager plan: %v", err)
			}
		})
	}
}

func TestUnsafeOrUnsupportedExtendedMetadata(t *testing.T) {
	for _, files := range []map[string][]byte{
		{"pom.xml": []byte("<!DOCTYPE foo [<!ENTITY xxe SYSTEM 'file:///etc/passwd'>]><project>&xxe;</project>")},
		{"App.csproj": []byte(`<Project><PropertyGroup><TargetFramework>net10.0-windows</TargetFramework></PropertyGroup></Project>`)},
		{"App.csproj": []byte(`<Project><PropertyGroup><TargetFramework>net8.0-android</TargetFramework></PropertyGroup></Project>`)},
		{"App.csproj": []byte(`<Project><PropertyGroup><TargetFramework>net6.0</TargetFramework></PropertyGroup></Project>`)},
		{"App.csproj": []byte(`<Project`)},
		{"App.csproj": []byte(`<Project/>`), "global.json": []byte(`{"sdk":{"version":"10.0;bad"}}`)},
		{"Gemfile": nil, ".ruby-version": []byte("3.4;bad")},
		{"composer.json": []byte("invalid")},
		{"package.json": []byte(`{"packageManager":"pnpm@https://evil.example/package.tgz"}`)},
		{"package.json": []byte(`{}`), "pnpm-lock.yaml": nil, "yarn.lock": nil},
		{"pyproject.toml": nil, "uv.lock": nil, "poetry.lock": nil},
	} {
		if p, err := Detect(files, "", ""); err == nil {
			t.Fatalf("unsafe/unsupported metadata accepted: %+v", p)
		}
	}
}

func TestCorepackIntegrityPinsArePreserved(t *testing.T) {
	for _, pin := range []string{"pnpm@10.20.0+sha512." + strings.Repeat("a", 128), "yarn@4.10.3+sha224." + strings.Repeat("b", 56)} {
		files := map[string][]byte{"package.json": []byte(`{"packageManager":"` + pin + `"}`)}
		plan, err := Detect(files, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if plan.Manager != pin || plan.Setup().Manager != pin {
			t.Fatalf("integrity pin lost: %+v", plan)
		}
		files["package.json"] = []byte(`{"devEngines":{"packageManager":{"name":"` + strings.Split(pin, "@")[0] + `","version":"` + strings.Split(pin, "@")[1] + `"}}}`)
		plan, err = Detect(files, "", "")
		if err != nil || plan.Manager != pin {
			t.Fatalf("devEngines pin lost: %+v %v", plan, err)
		}
	}
	for _, pin := range []string{"pnpm@10.20.0+sha512.a", "yarn@4.10.3+md5." + strings.Repeat("a", 32), "pnpm@10.20.0+sha224." + strings.Repeat("g", 56), "pnpm@10.20.0+sha224." + strings.Repeat("a", 56) + ";bad"} {
		if _, err := Detect(map[string][]byte{"package.json": []byte(`{"packageManager":"` + pin + `"}`)}, "", ""); err == nil {
			t.Fatalf("invalid integrity pin accepted: %s", pin)
		}
	}
}

func TestGradleDoesNotSkipUnlistedNestedBuild(t *testing.T) {
	plan, err := Detect(map[string][]byte{
		"settings.gradle.kts":       []byte("include(\":app\")\nincludeBuild(\"unlisted\")\n"),
		"app/build.gradle.kts":      []byte(`plugins { java }`),
		"unlisted/build.gradle.kts": []byte(`plugins { java }`),
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range plan.Parts() {
		if part.Directory == "unlisted" && (part.Workspace != "" || len(part.Commands) == 0) {
			t.Fatalf("unlisted project was silently skipped: %+v", part)
		}
		if part.Directory == "app" && (part.Workspace != "." || len(part.Commands) != 0) {
			t.Fatalf("declared member installs twice: %+v", part)
		}
	}
}

func TestDiscoveryFindsNamedDotnetProjectsAndNeverEvaluatesBuildScripts(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"src/Api.csproj": "<Project/>", "ruby/Gemfile": "File.write('executed', 'bad')", "jvm/build.gradle": "new File('executed').text='bad'", "native/CMakeLists.txt": "execute_process(COMMAND touch executed)", "src/obj/generated.csproj": "invalid"} {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := ReadLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("discovery=%v", files)
	}
	p, err := Detect(files, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Parts()) != 4 {
		t.Fatalf("incomplete mixed plan: %+v", p)
	}
	for _, dir := range []string{"ruby", "jvm", "native"} {
		if _, err := os.Stat(filepath.Join(root, dir, "executed")); !os.IsNotExist(err) {
			t.Fatal("build script executed during inspection")
		}
	}
}

func TestNewWorkspaceManagersOwnMemberInstalls(t *testing.T) {
	for _, tt := range []struct {
		name, member string
		files        map[string][]byte
	}{
		{"pnpm", "packages/app", map[string][]byte{"package.json": []byte(`{"packageManager":"pnpm@10.20.0"}`), "pnpm-workspace.yaml": []byte("packages:\n - packages/*\n"), "packages/app/package.json": []byte(`{"engines":{"node":">=22"}}`)}},
		{"uv", "packages/app", map[string][]byte{"pyproject.toml": []byte("[tool.uv.workspace]\nmembers=['packages/*']\n"), "packages/app/pyproject.toml": []byte("[project]\nrequires-python='>=3.12'\n")}},
		{"maven", "app", map[string][]byte{"pom.xml": []byte("<project><modules><module>app</module></modules></project>"), "app/pom.xml": []byte("<project/>")}},
		{"gradle", "app", map[string][]byte{"settings.gradle.kts": []byte(`include("app")`), "app/build.gradle.kts": []byte(`plugins { java }`)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Detect(tt.files, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Parts()) != 2 {
				t.Fatalf("missing member: %+v", p)
			}
			for _, part := range p.Parts() {
				if part.Directory == tt.member && (part.Workspace != "." || len(part.Commands) != 0) {
					t.Fatalf("member installer duplicated: %+v", part)
				}
			}
			again, err := DetectConfigured(tt.files, p.Setup())
			if err != nil || again.Fingerprint != p.Fingerprint {
				t.Fatalf("unstable workspace plan: %v", err)
			}
		})
	}
}

func TestRuntimeVersionAndExactImagePins(t *testing.T) {
	if version, err := ParseRuntimeVersion("java", "javac 21.0.12.1"); err != nil || version != "21.0.12" {
		t.Fatalf("Java security-update version: %q %v", version, err)
	}
	if _, err := ParseRuntimeVersion("java", "javac 21.0.12.bad"); err == nil {
		t.Fatal("invalid Java version accepted")
	}
	for _, tt := range []struct{ manifest, body, runtime string }{
		{"Gemfile", "ruby '3.3.6'", "3.3.6"},
		{"Gemfile", "ruby '~> 3.3.0'", "3.3"},
		{"Gemfile", "ruby '>= 3.2', '< 3.4'", "3.3"},
		{"composer.json", `{"require":{"php":"~8.2"}}`, "8.4"},
		{"composer.json", `{"require":{"php":"8.3.7"}}`, "8.3.7"},
		{"package.json", `{"engines":{"node":"22.14.0"}}`, "22.14.0"},
	} {
		p, err := Detect(map[string][]byte{tt.manifest: []byte(tt.body)}, "", "")
		if err != nil || p.Runtime != tt.runtime {
			t.Fatalf("%s: %+v %v", tt.body, p, err)
		}
	}
	if _, err := Detect(map[string][]byte{"CMakeLists.txt": []byte("project(app)"), "vcpkg.json": []byte(`{}`)}, "", ""); err == nil {
		t.Fatal("native dependency manager silently ignored")
	}
}
