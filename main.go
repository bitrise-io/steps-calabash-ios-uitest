package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bitrise-core/bitrise-init/utility"
	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-utils/command/rubycommand"
	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-xcode/simulator"
	shellquote "github.com/kballard/go-shellquote"
)

// ConfigsModel ...
type ConfigsModel struct {
	WorkDir     string
	GemFilePath string
	AppPath     string
	Options     string

	SimulatorDevice    string
	SimulatorOsVersion string

	CalabashCucumberVersion string
}

func createConfigsModelFromEnvs() ConfigsModel {
	return ConfigsModel{
		WorkDir:     os.Getenv("work_dir"),
		GemFilePath: os.Getenv("gem_file_path"),
		AppPath:     os.Getenv("app_path"),
		Options:     os.Getenv("additional_options"),

		SimulatorDevice:    os.Getenv("simulator_device"),
		SimulatorOsVersion: os.Getenv("simulator_os_version"),

		CalabashCucumberVersion: os.Getenv("calabash_cucumber_version"),
	}
}

func (configs ConfigsModel) print() {
	log.Infof("Configs:")
	log.Printf("- WorkDir: %s", configs.WorkDir)
	log.Printf("- GemFilePath: %s", configs.GemFilePath)
	log.Printf("- AppPath: %s", configs.AppPath)
	log.Printf("- Options: %s", configs.Options)

	log.Printf("- SimulatorDevice: %s", configs.SimulatorDevice)
	log.Printf("- SimulatorOsVersion: %s", configs.SimulatorOsVersion)

	log.Printf("- CalabashCucumberVersion: %s", configs.CalabashCucumberVersion)
}

func (configs ConfigsModel) validate() error {
	if configs.WorkDir == "" {
		return errors.New("no WorkDir parameter specified")
	}
	if exist, err := pathutil.IsDirExists(configs.WorkDir); err != nil {
		return fmt.Errorf("failed to check if WorkDir exist, error: %s", err)
	} else if !exist {
		return fmt.Errorf("WorkDir directory not exists at: %s", configs.WorkDir)
	}

	if configs.AppPath != "" {
		if exist, err := pathutil.IsDirExists(configs.AppPath); err != nil {
			return fmt.Errorf("failed to check if AppPath exist, error: %s", err)
		} else if !exist {
			return fmt.Errorf("AppPath directory not exists at: %s", configs.AppPath)
		}
	}

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

func copyDir(src, dst string, contentOnly bool) error {
	if !contentOnly {
		return os.Rename(src, dst)
	}

	files, err := utility.ListPathInDirSortedByComponents(src, false)
	if err != nil {
		return err
	}

	for _, file := range files {
		base := filepath.Base(file)
		pth := filepath.Join(dst, base)

		if err := os.Rename(file, pth); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	configs := createConfigsModelFromEnvs()

	fmt.Println()
	configs.print()

	if err := configs.validate(); err != nil {
		registerFail("Issue with input: %s", err)
	}

	options, err := shellquote.Split(configs.Options)
	if err != nil {
		registerFail("Failed to split additional options (%s), error: %s", configs.Options, err)
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
	// ---

	// Ensure if app is compatible with simulator device
	if configs.AppPath != "" {
		monotouch32Dir := filepath.Join(configs.AppPath, ".monotouch-32")
		monotouch32DirExist, err := pathutil.IsDirExists(monotouch32Dir)
		if err != nil {
			registerFail("Failed to check if path (%s) exist, error: %s", monotouch32Dir, err)
		}

		monotouch64Dir := filepath.Join(configs.AppPath, ".monotouch-64")
		monotouch64DirExist, err := pathutil.IsDirExists(monotouch64Dir)
		if err != nil {
			registerFail("Failed to check if path (%s) exist, error: %s", monotouch64Dir, err)
		}

		if monotouch32DirExist && monotouch64DirExist {
			fmt.Println()
			log.Warnf("The .app file generated for 'i386 + x86_64' architecture")

			is64Bit, err := simulator.Is64BitArchitecture(configs.SimulatorDevice)
			if err != nil {
				registerFail("Failed to check simulator architecture, error: %s", err)
			}

			log.Warnf("Simulator is 64-bit architecture: %v", is64Bit)

			tmpDir, err := pathutil.NormalizedOSTempDirPath("_calabash_ios_")
			if err != nil {
				registerFail("Failed to create tmp dir, error: %s", err)
			}

			appName := filepath.Base(configs.AppPath)
			newAppPath := filepath.Join(tmpDir, appName)

			log.Warnf("Creating compatible .app file at: %s", newAppPath)

			if err := command.CopyDir(configs.AppPath, tmpDir, false); err != nil {
				registerFail("Failed to copy .app to (%s), error: %s", newAppPath, err)
			}

			newAppMonotouch32Dir := filepath.Join(newAppPath, ".monotouch-32")
			newAppMonotouch64Dir := filepath.Join(newAppPath, ".monotouch-64")

			if is64Bit {
				log.Warnf("Copy files from .monotouch-64 dir...")

				if err := command.CopyDir(newAppMonotouch64Dir, newAppPath, true); err != nil {
					registerFail("Failed to copy .monotouch-64 files, error: %s", err)
				}
			} else {
				log.Warnf("Copy files from .monotouch-32 dir...")

				if err := command.CopyDir(newAppMonotouch32Dir, newAppPath, true); err != nil {
					registerFail("Failed to copy .monotouch-32 files, error: %s", err)
				}
			}

			configs.AppPath = newAppPath
		}
	}
	// ---

	//
	// Determining calabash-cucumber version
	fmt.Println()
	log.Infof("Determining calabash-cucumber version...")

	workDir, err := pathutil.AbsPath(configs.WorkDir)
	if err != nil {
		registerFail("Failed to expand WorkDir (%s), error: %s", configs.WorkDir, err)
	}

	gemFilePath := ""
	if configs.GemFilePath != "" {
		gemFilePath, err = pathutil.AbsPath(configs.GemFilePath)
		if err != nil {
			registerFail("Failed to expand GemFilePath (%s), error: %s", configs.GemFilePath, err)
		}
	}

	useBundler := false

	if gemFilePath != "" {
		if exist, err := pathutil.IsPathExists(gemFilePath); err != nil {
			registerFail("Failed to check if Gemfile exists at (%s) exist, error: %s", gemFilePath, err)
		} else if exist {
			log.Printf("Gemfile exists at: %s", gemFilePath)

			gemfileDir := filepath.Dir(gemFilePath)
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

				useBundler = true
			} else {
				log.Warnf("Gemfile.lock doest no find with calabash-cucumber gem at: %s", gemfileLockPth)
			}
		} else {
			log.Warnf("Gemfile doest no find with calabash-cucumber gem at: %s", gemFilePath)
		}
	}

	if configs.CalabashCucumberVersion != "" {
		log.Donef("using calabash-cucumber version: %s", configs.CalabashCucumberVersion)
	} else if useBundler {
		log.Donef("using calabash-cucumber with bundler")
	} else {
		log.Donef("using calabash-cucumber latest version")
	}

	// ---

	//
	// Intsalling cucumber gem
	fmt.Println()
	log.Infof("Installing calabash-cucumber...")

	if configs.CalabashCucumberVersion != "" {
		installed, err := rubycommand.IsGemInstalled("calabash-cucumber", configs.CalabashCucumberVersion)
		if err != nil {
			registerFail("Failed to check if calabash-cucumber (v%s) installed, error: %s", configs.CalabashCucumberVersion, err)
		}

		if !installed {
			installCommands, err := rubycommand.GemInstall("calabash-cucumber", configs.CalabashCucumberVersion)
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
			log.Printf("calabash-cucumber %s installed", configs.CalabashCucumberVersion)
		}
	} else if useBundler {
		bundleInstallCmd, err := rubycommand.New("bundle", "install", "--jobs", "20", "--retry", "5")
		if err != nil {
			registerFail("Failed to create command, error: %s", err)
		}

		bundleInstallCmd.AppendEnvs("BUNDLE_GEMFILE=" + gemFilePath)
		bundleInstallCmd.SetStdout(os.Stdout).SetStderr(os.Stderr)

		log.Printf("$ %s", bundleInstallCmd.PrintableCommandArgs())

		if err := bundleInstallCmd.Run(); err != nil {
			registerFail("bundle install failed, error: %s", err)
		}
	} else {
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

	//
	// Run cucumber
	fmt.Println()
	log.Infof("Running cucumber test...")

	cucumberEnvs := []string{"DEVICE_TARGET=" + simulatorInfo.ID}
	if configs.AppPath != "" {
		cucumberEnvs = append(cucumberEnvs, "APP="+configs.AppPath)
	}

	cucumberArgs := []string{"cucumber"}
	if configs.CalabashCucumberVersion != "" {
		cucumberArgs = append(cucumberArgs, fmt.Sprintf("_%s_", configs.CalabashCucumberVersion))
	} else if useBundler {
		cucumberArgs = append([]string{"bundle", "exec"}, cucumberArgs...)
		cucumberEnvs = append(cucumberEnvs, "BUNDLE_GEMFILE="+gemFilePath)
	}

	cucumberArgs = append(cucumberArgs, options...)

	cucumberCmd, err := rubycommand.NewFromSlice(cucumberArgs...)
	if err != nil {
		registerFail("Failed to create command, error: %s", err)
	}

	cucumberCmd.AppendEnvs(cucumberEnvs...)
	cucumberCmd.SetDir(workDir)
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
