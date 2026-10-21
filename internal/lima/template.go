package lima

import (
	_ "embed"

	"github.com/saeta-eth/watermelon/internal/config"
)

//go:embed templates/lima.yaml.tmpl
var limaTemplate string

type provisionBuild struct {
	BaseImage   string
	CustomTag   string
	InstallArgs []string
	BinDirs     string
	ExposeBins  []string
}

type smartWrapper struct {
	Cmd         string // "npm", "pip", etc.
	BaseImage   string // original container image
	CustomTag   string // "watermelon-npm", etc.
	BinDirs     string // dirs to scan for new binaries
	GlobalCheck string // shell snippet setting _wm_global=true
}

type provisionScript struct {
	Name   string
	Base64 string
}

type mountData struct {
	Location string
	Target   string
	Writable bool
}

type templateData struct {
	WorkspaceDisk         string
	FormatWorkspaceDisk   bool
	SetupImage            string
	SetupCommands         []string
	SetupPython           bool
	SetupGo               bool
	SetupRust             bool
	SetupRustImage        string
	SetupBaseImage        string
	SetupComposeSources   []string
	SetupDefaultVenv      string
	SetupExtendedAssembly bool
	SetupAssemblyCopies   []runtimeCopy
	SetupRuntimeAssets    []runtimeAsset
	SetupJava             bool
	SetupDotnet           bool
	SetupRuby             bool
	SetupPHP              bool
	SetupCPP              bool
	SetupPHPImage         string

	MinimumLimaVersion   string
	VMType               string
	VMUser               string
	VMUserUID            uint32
	VMUserHome           string
	CPUs                 int
	Memory               string
	Disk                 string
	Image                string
	MountProject         bool
	ProjectDir           string
	Workdir              string
	DynamicWorkdir       bool
	ContainerWorkdirArgs string
	HelperWorkdirArgs    string
	BootstrapHostDir     string
	LogHostDir           string
	LogDir               string
	LogPath              string
	LogInGuest           bool
	Mounts               []mountData
	NetworkRules         []config.NetworkRule
	NetworkProcessRules  map[string][]config.NetworkRule
	NetworkProcessImages map[string]string // process name → container image (when known at config time)
	ProcessKernelIDs     map[string]string // process name → short stable identifier for kernel objects
	PortForwards         []int
	Tools                map[string][]string
	BootstrapImages      []string
	ProvisionBuilds      []provisionBuild
	SmartWrappers        []smartWrapper
	SortedProcessNames   []string // process names sorted alphabetically
	UniqueImages         []string // all container images that need to be in rootful store
	Enforcement          string
	LogUnknown           bool
	RejectUnknown        bool
	VerdictServerPort    int
	VerdictAuthKey       string
	VerdictAuthKeyPath   string
	NfqdBinaryPath       string
	NfqdSHA256           string
	GlobalWildcardRules  []config.NetworkRule // wildcard domains from NetworkRules
	DNSExactHosts        []string
	DNSWildcardHosts     []string
	ProcessDNSExactHosts map[string][]string
	ProcessDNSWildcards  map[string][]string
	DNSAllowlistOnly     bool
	ProvisionScripts     []provisionScript
}
