package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bitrise-io/go-utils/cmdex"
	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-steplib/steps-calabash-ios-uitest/rubycmd"
	"github.com/bitrise-tools/go-xcode/simulator"
	version "github.com/hashicorp/go-version"
)

// ConfigsModel ...
type ConfigsModel struct {
	SimulatorDevice         string
	SimulatorOsVersion      string
	CalabashCucumberVersion string
	GemFilePath             string
}

func createConfigsModelFromEnvs() ConfigsModel {
	return ConfigsModel{
		SimulatorDevice:         os.Getenv("simulator_device"),
		SimulatorOsVersion:      os.Getenv("simulator_os_version"),
		CalabashCucumberVersion: os.Getenv("calabash_cucumber_version"),
		GemFilePath:             os.Getenv("gem_file_path"),
	}
}

func (configs ConfigsModel) print() {
	log.Info("Configs:")
	log.Detail("- SimulatorDevice: %s", configs.SimulatorDevice)
	log.Detail("- SimulatorOsVersion: %s", configs.SimulatorOsVersion)
	log.Detail("- CalabashCucumberVersion: %s", configs.CalabashCucumberVersion)
	log.Detail("- GemFilePath: %s", configs.GemFilePath)
}

func (configs ConfigsModel) validate() error {
	if configs.SimulatorDevice == "" {
		return errors.New("No SimulatorDevice parameter specified!")
	}
	if configs.SimulatorOsVersion == "" {
		return errors.New("No SimulatorOsVersion parameter specified!")
	}

	return nil
}

func exportEnvironmentWithEnvman(keyStr, valueStr string) error {
	cmd := cmdex.NewCommand("envman", "add", "--key", keyStr)
	cmd.SetStdin(strings.NewReader(valueStr))
	return cmd.Run()
}

func getLatestIOSVersion(osVersionSimulatorInfosMap simulator.OsVersionSimulatorInfosMap) (string, error) {
	var latestVersionPtr *version.Version
	for osVersion := range osVersionSimulatorInfosMap {
		if !strings.HasPrefix(osVersion, "iOS") {
			continue
		}

		versionStr := strings.TrimPrefix(osVersion, "iOS")
		versionStr = strings.TrimSpace(versionStr)

		versionPtr, err := version.NewVersion(versionStr)
		if err != nil {
			return "", fmt.Errorf("Failed to parse version (%s), error: %s", versionStr, err)
		}

		if latestVersionPtr == nil || versionPtr.GreaterThan(latestVersionPtr) {
			latestVersionPtr = versionPtr
		}
	}

	if latestVersionPtr == nil {
		return "", fmt.Errorf("Failed to determin latest iOS simulator version")
	}

	versionSegments := latestVersionPtr.Segments()
	if len(versionSegments) < 2 {
		return "", fmt.Errorf("Invalid version created: %s, segments count < 2", latestVersionPtr.String())
	}

	return fmt.Sprintf("iOS %d.%d", versionSegments[0], versionSegments[1]), nil
}

func getSimulatorInfo(osVersion, deviceName string) (simulator.InfoModel, error) {
	osVersionSimulatorInfosMap, err := simulator.GetOsVersionSimulatorInfosMap()
	if err != nil {
		return simulator.InfoModel{}, err
	}

	if osVersion == "latest" {
		latestOSVersion, err := getLatestIOSVersion(osVersionSimulatorInfosMap)
		if err != nil {
			return simulator.InfoModel{}, err
		}
		osVersion = latestOSVersion
	}

	infos, ok := osVersionSimulatorInfosMap[osVersion]
	if !ok {
		return simulator.InfoModel{}, fmt.Errorf("No simulators found for os version: %s", osVersion)
	}

	for _, info := range infos {
		if info.Name == deviceName {
			return info, nil
		}
	}

	return simulator.InfoModel{}, fmt.Errorf("No simulators found for os version: (%s), device name: (%s)", osVersion, deviceName)
}

func registerFail(format string, v ...interface{}) {
	log.Error(format, v...)

	if err := exportEnvironmentWithEnvman("BITRISE_XAMARIN_TEST_RESULT", "failed"); err != nil {
		log.Warn("Failed to export environment: %s, error: %s", "BITRISE_XAMARIN_TEST_RESULT", err)
	}

	os.Exit(1)
}

func calabashCucumberFromGemfileLockContent(content string) string {
	relevantLines := []string{}
	lines := strings.Split(content, "\n")

	specsStart := false
	for _, line := range lines {
		if strings.Contains(line, "specs:") {
			specsStart = true
		}

		trimmed := strings.Trim(line, " ")
		if trimmed == "" {
			break
		}

		if specsStart {
			relevantLines = append(relevantLines, line)
		}
	}

	exp := regexp.MustCompile(`calabash-cucumber \((.+)\)`)
	for _, line := range relevantLines {
		match := exp.FindStringSubmatch(line)
		if match != nil && len(match) == 2 {
			return match[1]
		}
	}

	return ""
}

func calabashCucumberVersionFromGemfileLock(gemfileLockPth string) (string, error) {
	content, err := fileutil.ReadStringFromFile(gemfileLockPth)
	if err != nil {
		return "", err
	}
	return calabashCucumberFromGemfileLockContent(content), nil
}

func main() {
	configs := createConfigsModelFromEnvs()

	fmt.Println()
	configs.print()

	if err := configs.validate(); err != nil {
		registerFail("Issue with input: %s", err)
	}

	// Get Simulator Infos
	fmt.Println()
	log.Info("Collecting simulator info...")

	simulatorInfo, err := getSimulatorInfo(configs.SimulatorOsVersion, configs.SimulatorDevice)
	if err != nil {
		registerFail("Failed to get simulator infos, error: %s", err)
	}
	log.Done("Simulator (%s), id: (%s), status: %s", simulatorInfo.Name, simulatorInfo.ID, simulatorInfo.Status)

	if err := os.Setenv("DEVICE_TARGET", simulatorInfo.ID); err != nil {
		registerFail("Failed to set DEVICE_TARGET environment, error: %s", err)
	}
	// ---

	//
	// Determining calabash-cucumber version
	fmt.Println()
	log.Info("Determining calabash-cucumber version...")

	rubyCommand, err := rubycmd.NewRubyCommandModel()
	if err != nil {
		registerFail("Failed to create ruby command, err: %s", err)
	}

	calabashCucumberVersion := ""
	useBundler := false

	if configs.GemFilePath != "" {
		if exist, err := pathutil.IsPathExists(configs.GemFilePath); err != nil {
			registerFail("Failed to check if Gemfile exists at (%s) exist, error: %s", configs.GemFilePath, err)
		} else if exist {
			log.Detail("Gemfile exists at: %s", configs.GemFilePath)

			gemfileDir := filepath.Dir(configs.GemFilePath)
			gemfileLockPth := filepath.Join(gemfileDir, "Gemfile.lock")

			if exist, err := pathutil.IsPathExists(gemfileLockPth); err != nil {
				registerFail("Failed to check if Gemfile.lock exists at (%s), error: %s", gemfileLockPth, err)
			} else if exist {
				log.Detail("Gemfile.lock exists at: %s", gemfileLockPth)

				version, err := calabashCucumberVersionFromGemfileLock(gemfileLockPth)
				if err != nil {
					registerFail("Failed to get calabash-cucumber version from Gemfile.lock, error: %s", err)
				}

				log.Detail("calabash-cucumber version in Gemfile.lock: %s", version)

				calabashCucumberVersion = version
				useBundler = true
			} else {
				log.Warn("Gemfile.lock doest no find with calabash-cucumber gem at: %s", gemfileLockPth)
			}
		} else {
			log.Warn("Gemfile doest no find with calabash-cucumber gem at: %s", configs.GemFilePath)
		}
	}

	if configs.CalabashCucumberVersion != "" {
		log.Detail("calabash-cucumber version in configs: %s", configs.CalabashCucumberVersion)

		calabashCucumberVersion = configs.CalabashCucumberVersion
		useBundler = false
	}

	log.Done("using calabash-cucumber version: %s", calabashCucumberVersion)
	// ---

	//
	// Intsalling cucumber gem
	fmt.Println()
	log.Info("Installing calabash-cucumber gem...")

	cucumberArgs := []string{}

	// If Gemfile given with calabash-cucumber and calabash_cucumber_version input does not override cucumber version
	// Run `bundle install`
	// Run cucumber with `bundle exec`
	if useBundler {
		bundleInstallArgs := []string{"bundle", "install", "--jobs", "20", "--retry", "5"}

		// bundle install
		bundleInstallCmd, err := rubyCommand.Command(false, bundleInstallArgs)
		if err != nil {
			registerFail("Failed to create command, error: %s", err)
		}

		bundleInstallCmd.AppendEnvs([]string{"BUNDLE_GEMFILE=" + configs.GemFilePath})

		log.Detail("$ %s", cmdex.PrintableCommandArgs(false, bundleInstallArgs))

		if err := bundleInstallCmd.Run(); err != nil {
			registerFail("bundle install failed, error: %s", err)
		}
		// ---

		cucumberArgs = []string{"bundle", "exec"}
	}

	cucumberArgs = append(cucumberArgs, "cucumber")

	// If no need to use bundler
	if !useBundler {
		if calabashCucumberVersion != "" {
			// ... and cucumber version detected
			// Install calabash-cucumber detetcted version with `gem install`
			// Append version param to cucumber command
			installed, err := rubyCommand.IsGemInstalled("calabash-cucumber", calabashCucumberVersion)
			if err != nil {
				registerFail("Failed to check if calabash-cucumber (v%s) installed, error: %s", calabashCucumberVersion, err)
			}

			if !installed {
				installCommands, err := rubyCommand.GemInstallCommands("calabash-cucumber", calabashCucumberVersion)
				if err != nil {
					registerFail("Failed to create gem install commands, error: %s", err)
				}

				for _, installCommand := range installCommands {
					log.Detail("$ %s", cmdex.PrintableCommandArgs(false, installCommand.GetCmd().Args))

					installCommand.SetStdout(os.Stdout)
					installCommand.SetStderr(os.Stderr)

					if err := installCommand.Run(); err != nil {
						registerFail("command failed, error: %s", err)
					}
				}
			} else {
				log.Detail("calabash-cucumber %s installed", calabashCucumberVersion)
			}
		} else {
			// ... and using latest version of cucumber
			// Install calabash-cucumber latest version with `gem install`

			installCommands, err := rubyCommand.GemInstallCommands("calabash-cucumber", "")
			if err != nil {
				registerFail("Failed to create gem install commands, error: %s", err)
			}

			for _, installCommand := range installCommands {
				args := []string{installCommand.GetCmd().Path}
				args = append(args, installCommand.GetCmd().Args...)

				log.Detail("$ %s", cmdex.PrintableCommandArgs(false, args))

				if err := installCommand.Run(); err != nil {
					registerFail("command failed, error: %s", err)
				}
			}
		}
	}
	// ---

	//
	// Run cucumber
	fmt.Println()
	log.Info("Running cucumber test...")

	cucumberCmd, err := rubyCommand.Command(useBundler, cucumberArgs)
	if err != nil {
		registerFail("Failed to create command, error: %s", err)
	}

	cucumberEnvs := []string{"DEVICE_TARGET=" + simulatorInfo.ID}
	if useBundler {
		cucumberEnvs = append(cucumberEnvs, "BUNDLE_GEMFILE="+configs.GemFilePath)
	}

	cucumberCmd.AppendEnvs(cucumberEnvs)

	cucumberCmd.SetStdout(os.Stdout)
	cucumberCmd.SetStderr(os.Stderr)

	log.Detail("$ %s", cmdex.PrintableCommandArgs(false, cucumberArgs))

	fmt.Println()
	if err := cucumberCmd.Run(); err != nil {
		registerFail("cucumber failed, error: %s", err)
	}
	// ---

}
