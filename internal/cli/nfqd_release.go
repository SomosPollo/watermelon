package cli

import (
	"crypto/sha256"
	debugbuildinfo "debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	runtimedebug "runtime/debug"
	"strings"
	"time"
)

const (
	watermelonModulePath       = "github.com/saeta-eth/watermelon"
	watermelonCLIPath          = watermelonModulePath + "/cmd/watermelon"
	watermelonNfqdPath         = watermelonModulePath + "/cmd/watermelon-nfqd"
	githubReleaseTagsAPI       = "https://api.github.com/repos/SomosPollo/watermelon/releases/tags"
	maxReleaseMetadataBytes    = 2 << 20
	maxNfqdReleaseAssetBytes   = 64 << 20
	githubAPIVersion           = "2022-11-28"
	nfqdReleaseDownloadTimeout = 2 * time.Minute
)

var (
	releaseTagPattern     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
	pseudoVersionTail     = regexp.MustCompile(`(?:-|[-.]0\.)[0-9]{14}-[0-9a-f]{12,}$`)
	readNfqdBuildInfoFile = debugbuildinfo.ReadFile
)

type nfqdReleaseSource struct {
	client        *http.Client
	releaseAPIURL string
	readBuildInfo func(string) (*runtimedebug.BuildInfo, error)
}

type githubReleaseMetadata struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

func newNfqdReleaseSource() nfqdReleaseSource {
	return nfqdReleaseSource{
		client: &http.Client{
			Timeout: nfqdReleaseDownloadTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" {
					return errors.New("refusing a non-HTTPS release redirect")
				}
				if len(via) >= 10 {
					return errors.New("too many release download redirects")
				}
				return nil
			},
		},
		releaseAPIURL: githubReleaseTagsAPI,
		readBuildInfo: readNfqdBuildInfoFile,
	}
}

func goInstallReleaseVersion(readBuildInfo func() (*runtimedebug.BuildInfo, bool)) (string, bool) {
	if readBuildInfo == nil {
		return "", false
	}
	info, ok := readBuildInfo()
	if !ok || info == nil || info.Path != watermelonCLIPath || info.Main.Path != watermelonModulePath || info.Main.Replace != nil {
		return "", false
	}
	version := info.Main.Version
	if !releaseTagPattern.MatchString(version) || pseudoVersionTail.MatchString(version) || strings.HasSuffix(version, "+dirty") {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return "", false
		}
	}
	return version, true
}

func (source nfqdReleaseSource) install(dest, version, arch string) error {
	if source.client == nil || source.readBuildInfo == nil {
		return errors.New("network interceptor release source is not configured")
	}
	if !releaseTagPattern.MatchString(version) || pseudoVersionTail.MatchString(version) || strings.HasSuffix(version, "+dirty") {
		return fmt.Errorf("%q is not a release tag", version)
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("release network interceptors are not available for architecture %q", arch)
	}

	assetName := "watermelon-nfqd-linux-" + arch
	asset, err := source.lookupAsset(version, assetName)
	if err != nil {
		return err
	}
	return installBuiltNfqd(dest, func(outputPath string) error {
		if err := source.downloadAsset(outputPath, asset); err != nil {
			return err
		}
		return source.validateFile(outputPath, version, arch)
	})
}

func (source nfqdReleaseSource) validateFile(path, version, arch string) error {
	if source.readBuildInfo == nil {
		return errors.New("network interceptor build-information reader is not configured")
	}
	info, err := source.readBuildInfo(path)
	if err != nil {
		return fmt.Errorf("reading network interceptor build information: %w", err)
	}
	return validateNfqdReleaseBuild(info, version, arch)
}

func (source nfqdReleaseSource) lookupAsset(version, assetName string) (githubReleaseAsset, error) {
	releaseURL := strings.TrimRight(source.releaseAPIURL, "/") + "/" + url.PathEscape(version)
	req, err := http.NewRequest(http.MethodGet, releaseURL, nil)
	if err != nil {
		return githubReleaseAsset{}, fmt.Errorf("creating release metadata request: %w", err)
	}
	setGitHubReleaseHeaders(req, version)
	resp, err := source.client.Do(req)
	if err != nil {
		return githubReleaseAsset{}, fmt.Errorf("requesting release metadata for %s: %w", version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return githubReleaseAsset{}, responseStatusError("requesting release metadata", resp)
	}
	data, err := readLimitedBody(resp.Body, maxReleaseMetadataBytes)
	if err != nil {
		return githubReleaseAsset{}, fmt.Errorf("reading release metadata: %w", err)
	}
	var metadata githubReleaseMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return githubReleaseAsset{}, fmt.Errorf("decoding release metadata: %w", err)
	}
	if metadata.TagName != version {
		return githubReleaseAsset{}, fmt.Errorf("release metadata returned tag %q, want %q", metadata.TagName, version)
	}
	for _, asset := range metadata.Assets {
		if asset.Name != assetName {
			continue
		}
		if asset.Size < 1 || asset.Size > maxNfqdReleaseAssetBytes {
			return githubReleaseAsset{}, fmt.Errorf("release asset %q has unsafe size %d", assetName, asset.Size)
		}
		if _, err := parseSHA256Digest(asset.Digest); err != nil {
			return githubReleaseAsset{}, fmt.Errorf("release asset %q: %w", assetName, err)
		}
		assetURL, err := url.Parse(asset.BrowserDownloadURL)
		if err != nil || assetURL.Scheme != "https" || assetURL.Host == "" {
			return githubReleaseAsset{}, fmt.Errorf("release asset %q has an invalid HTTPS download URL", assetName)
		}
		return asset, nil
	}
	return githubReleaseAsset{}, fmt.Errorf("release %s does not contain %s", version, assetName)
}

func (source nfqdReleaseSource) downloadAsset(outputPath string, asset githubReleaseAsset) error {
	req, err := http.NewRequest(http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("creating network interceptor download request: %w", err)
	}
	setGitHubReleaseHeaders(req, "")
	resp, err := source.client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil || resp.Request.URL.Scheme != "https" {
		return fmt.Errorf("downloading %s: final response did not use HTTPS", asset.Name)
	}
	if resp.StatusCode != http.StatusOK {
		return responseStatusError("downloading "+asset.Name, resp)
	}
	if resp.ContentLength > maxNfqdReleaseAssetBytes {
		return fmt.Errorf("downloading %s: response is too large", asset.Name)
	}

	wantDigest, err := parseSHA256Digest(asset.Digest)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("opening temporary network interceptor: %w", err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(resp.Body, maxNfqdReleaseAssetBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("downloading %s: %w", asset.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("saving %s: %w", asset.Name, closeErr)
	}
	if written > maxNfqdReleaseAssetBytes {
		return fmt.Errorf("downloading %s: response is too large", asset.Name)
	}
	if written != asset.Size {
		return fmt.Errorf("downloading %s: received %d bytes, metadata declared %d", asset.Name, written, asset.Size)
	}
	if got := hasher.Sum(nil); !equalDigest(got, wantDigest) {
		return fmt.Errorf("downloading %s: SHA-256 digest does not match GitHub release metadata", asset.Name)
	}
	return nil
}

func validateNfqdReleaseBuild(info *runtimedebug.BuildInfo, version, arch string) error {
	if info == nil {
		return errors.New("downloaded network interceptor has no Go build information")
	}
	if info.Path != watermelonNfqdPath || info.Main.Path != watermelonModulePath || info.Main.Replace != nil {
		return errors.New("downloaded network interceptor is not the official Watermelon nfqd command")
	}
	if info.Main.Version != version {
		return fmt.Errorf("downloaded network interceptor module version is %q, want %q", info.Main.Version, version)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != arch {
		return fmt.Errorf("downloaded network interceptor target is %s/%s, want linux/%s", settings["GOOS"], settings["GOARCH"], arch)
	}
	if settings["CGO_ENABLED"] != "0" {
		return errors.New("downloaded network interceptor is not the expected static release build")
	}
	if settings["vcs.modified"] == "true" {
		return errors.New("downloaded network interceptor was built from modified source")
	}
	return nil
}

func parseSHA256Digest(digest string) ([]byte, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) {
		return nil, errors.New("has no SHA-256 digest in GitHub release metadata")
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(digest, prefix))
	if err != nil || len(decoded) != sha256.Size {
		return nil, errors.New("has an invalid SHA-256 digest in GitHub release metadata")
	}
	return decoded, nil
}

func equalDigest(got, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	var different byte
	for i := range got {
		different |= got[i] ^ want[i]
	}
	return different == 0
}

func setGitHubReleaseHeaders(req *http.Request, version string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if version == "" {
		version = "unknown"
	}
	req.Header.Set("User-Agent", "watermelon/"+version)
}

func readLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func responseStatusError(action string, resp *http.Response) error {
	detail, _ := readLimitedBody(resp.Body, 512)
	message := strings.TrimSpace(string(detail))
	if message == "" {
		return fmt.Errorf("%s: GitHub returned %s", action, resp.Status)
	}
	return fmt.Errorf("%s: GitHub returned %s: %s", action, resp.Status, message)
}
