package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-utils/command/rubycommand"
	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-xcode/simulator"
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
	log.Infof("Configs:")
	log.Printf("- SimulatorDevice: %s", configs.SimulatorDevice)
	log.Printf("- SimulatorOsVersion: %s", configs.SimulatorOsVersion)
	log.Printf("- CalabashCucumberVersion: %s", configs.CalabashCucumberVersion)
	log.Printf("- GemFilePath: %s", configs.GemFilePath)
}

func (configs ConfigsModel) validate() error {
	if configs.SimulatorDevice == "" {
		return errors.New("no SimulatorDevice parameter specified")
	}
	if configs.SimulatorOsVersion == "" {
		return errors.New("no SimulatorOsVersion parameter specified")
	}

	return nil
}

func exportEnvironmentWithEnvman(keyStr, valueStr string) error {
	cmd := command.New("envman", "add", "--key", keyStr)
	cmd.SetStdin(strings.NewReader(valueStr))
	return cmd.Run()
}

func registerFail(format string, v ...interface{}) {
	log.Errorf(format, v...)

	if err := exportEnvironmentWithEnvman("BITRISE_XAMARIN_TEST_RESULT", "failed"); err != nil {
		log.Warnf("Failed to export environment: %s, error: %s", "BITRISE_XAMARIN_TEST_RESULT", err)
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
	log.Infof("Collecting simulator info...")

	var simulatorInfo simulator.InfoModel
	if configs.SimulatorOsVersion == "latest" {
		info, version, err := simulator.GetLatestSimulatorInfoAndVersion("iOS", configs.SimulatorDevice)
		if err != nil {
			registerFail("Failed to get simulator info, error: %s", err)
		}
		simulatorInfo = info

		log.Printf("Latest os version: %s", version)
	} else {
		info, err := simulator.GetSimulatorInfo(configs.SimulatorOsVersion, configs.SimulatorDevice)
		if err != nil {
			registerFail("Failed to get simulator info, error: %s", err)
		}
		simulatorInfo = info
	}

	log.Donef("Simulator (%s), id: (%s), status: %s", simulatorInfo.Name, simulatorInfo.ID, simulatorInfo.Status)

	if err := os.Setenv("DEVICE_TARGET", simulatorInfo.ID); err != nil {
		registerFail("Failed to set DEVICE_TARGET environment, error: %s", err)
	}
	// ---

	//
	// Determining calabash-cucumber version
	fmt.Println()
	log.Infof("Determining calabash-cucumber version...")

	calabashCucumberVersion := ""
	useBundler := false

	if configs.GemFilePath != "" {
		if exist, err := pathutil.IsPathExists(configs.GemFilePath); err != nil {
			registerFail("Failed to check if Gemfile exists at (%s) exist, error: %s", configs.GemFilePath, err)
		} else if exist {
			log.Printf("Gemfile exists at: %s", configs.GemFilePath)

			gemfileDir := filepath.Dir(configs.GemFilePath)
			gemfileLockPth := filepath.Join(gemfileDir, "Gemfile.lock")

			if exist, err := pathutil.IsPathExists(gemfileLockPth); err != nil {
				registerFail("Failed to check if Gemfile.lock exists at (%s), error: %s", gemfileLockPth, err)
			} else if exist {
				log.Printf("Gemfile.lock exists at: %s", gemfileLockPth)

				version, err := calabashCucumberVersionFromGemfileLock(gemfileLockPth)
				if err != nil {
					registerFail("Failed to get calabash-cucumber version from Gemfile.lock, error: %s", err)
				}

				log.Printf("calabash-cucumber version in Gemfile.lock: %s", version)

				calabashCucumberVersion = version
				useBundler = true
			} else {
				log.Warnf("Gemfile.lock doest no find with calabash-cucumber gem at: %s", gemfileLockPth)
			}
		} else {
			log.Warnf("Gemfile doest no find with calabash-cucumber gem at: %s", configs.GemFilePath)
		}
	}

	if configs.CalabashCucumberVersion != "" {
		log.Printf("calabash-cucumber version in configs: %s", configs.CalabashCucumberVersion)

		calabashCucumberVersion = configs.CalabashCucumberVersion
		useBundler = false
	}

	if calabashCucumberVersion == "" {
		log.Donef("using calabash-cucumber latest version")
	} else {
		log.Donef("using calabash-cucumber version: %s", calabashCucumberVersion)
	}
	// ---

	//
	// Intsalling cucumber gem
	fmt.Println()
	log.Infof("Installing calabash-cucumber gem...")

	cucumberArgs := []string{}

	// If Gemfile given with calabash-cucumber and calabash_cucumber_version input does not override cucumber version
	// Run `bundle install`
	// Run cucumber with `bundle exec`
	if useBundler {
		// bundle install
		bundleInstallCmd, err := rubycommand.New("bundle", "install", "--jobs", "20", "--retry", "5")
		if err != nil {
			registerFail("Failed to create command, error: %s", err)
		}

		bundleInstallCmd.AppendEnvs("BUNDLE_GEMFILE=" + configs.GemFilePath)
		bundleInstallCmd.SetStdout(os.Stdout).SetStderr(os.Stderr)

		log.Printf("$ %s", bundleInstallCmd.PrintableCommandArgs())

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
			installed, err := rubycommand.IsGemInstalled("calabash-cucumber", calabashCucumberVersion)
			if err != nil {
				registerFail("Failed to check if calabash-cucumber (v%s) installed, error: %s", calabashCucumberVersion, err)
			}

			if !installed {
				installCommands, err := rubycommand.GemInstall("calabash-cucumber", calabashCucumberVersion)
				if err != nil {
					registerFail("Failed to create gem install commands, error: %s", err)
				}

				for _, installCommand := range installCommands {
					log.Printf("$ %s", installCommand.PrintableCommandArgs())

					installCommand.SetStdout(os.Stdout).SetStderr(os.Stderr)

					if err := installCommand.Run(); err != nil {
						registerFail("command failed, error: %s", err)
					}
				}
			} else {
				log.Printf("calabash-cucumber %s installed", calabashCucumberVersion)
			}
		} else {
			// ... and using latest version of cucumber
			// Install calabash-cucumber latest version with `gem install`

			installCommands, err := rubycommand.GemInstall("calabash-cucumber", "")
			if err != nil {
				registerFail("Failed to create gem install commands, error: %s", err)
			}

			for _, installCommand := range installCommands {
				log.Printf("$ %s", installCommand.PrintableCommandArgs())

				installCommand.SetStdout(os.Stdout).SetStderr(os.Stderr)

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
	log.Infof("Running cucumber test...")

	cucumberCmd, err := rubycommand.NewFromSlice(cucumberArgs...)
	if err != nil {
		registerFail("Failed to create command, error: %s", err)
	}

	cucumberEnvs := []string{"DEVICE_TARGET=" + simulatorInfo.ID}
	if useBundler {
		cucumberEnvs = append(cucumberEnvs, "BUNDLE_GEMFILE="+configs.GemFilePath)
	}

	cucumberCmd.AppendEnvs(cucumberEnvs...)
	cucumberCmd.SetStdout(os.Stdout).SetStderr(os.Stderr)

	log.Printf("$ %s", cucumberCmd.PrintableCommandArgs())
	fmt.Println()

	if err := cucumberCmd.Run(); err != nil {
		registerFail("cucumber failed, error: %s", err)
	}
	// ---

	if err := exportEnvironmentWithEnvman("BITRISE_XAMARIN_TEST_RESULT", "succeeded"); err != nil {
		log.Warnf("Failed to export environment: %s, error: %s", "BITRISE_XAMARIN_TEST_RESULT", err)
	}
}
