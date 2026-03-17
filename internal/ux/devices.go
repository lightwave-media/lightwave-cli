package ux

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Device represents an AVFoundation capture device.
type Device struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

// UXConfig stores user preferences for recording.
type UXConfig struct {
	Screen       int    `json:"screen"`
	AudioDevice  int    `json:"audio_device"`
	WhisperModel string `json:"whisper_model"`
}

// ConfigPath returns the path to the UX config file.
func ConfigPath() string {
	return filepath.Join(BaseDir(), "config.json")
}

// LoadConfig loads the UX config, returning nil if not found.
func LoadConfig() (*UXConfig, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c UXConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveConfig writes the UX config to disk.
func SaveConfig(c *UXConfig) error {
	if err := os.MkdirAll(BaseDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0644)
}

// ListDevices runs ffmpeg to discover AVFoundation devices.
// Returns video devices and audio devices separately.
func ListDevices() ([]Device, []Device, error) {
	cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	// ffmpeg writes device list to stderr
	output, _ := cmd.CombinedOutput()

	lines := strings.Split(string(output), "\n")
	var videoDevices, audioDevices []Device
	inAudio := false

	// Pattern: [AVFoundation indev @ ...] [N] Device Name
	deviceRe := regexp.MustCompile(`\[AVFoundation.*\] \[(\d+)\] (.+)`)

	for _, line := range lines {
		if strings.Contains(line, "AVFoundation video devices:") {
			inAudio = false
			continue
		}
		if strings.Contains(line, "AVFoundation audio devices:") {
			inAudio = true
			continue
		}

		matches := deviceRe.FindStringSubmatch(line)
		if len(matches) == 3 {
			idx, _ := strconv.Atoi(matches[1])
			d := Device{Index: idx, Name: strings.TrimSpace(matches[2])}
			if inAudio {
				audioDevices = append(audioDevices, d)
			} else {
				videoDevices = append(videoDevices, d)
			}
		}
	}

	return videoDevices, audioDevices, nil
}

// PromptDeviceSelection interactively asks the user to pick screen and audio devices.
func PromptDeviceSelection() (*UXConfig, error) {
	videoDevices, audioDevices, err := ListDevices()
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	if len(videoDevices) == 0 {
		return nil, fmt.Errorf("no video capture devices found")
	}
	if len(audioDevices) == 0 {
		return nil, fmt.Errorf("no audio capture devices found")
	}

	// Filter to screen capture devices only
	var screens []Device
	for _, d := range videoDevices {
		if strings.HasPrefix(d.Name, "Capture screen") {
			screens = append(screens, d)
		}
	}
	if len(screens) == 0 {
		return nil, fmt.Errorf("no screen capture devices found (expected 'Capture screen N')")
	}

	reader := bufio.NewReader(os.Stdin)

	// Pick screen
	fmt.Println("\nAvailable screens:")
	for i, d := range screens {
		fmt.Printf("  [%d] %s\n", i, d.Name)
	}
	screenIdx := 0
	if len(screens) > 1 {
		fmt.Printf("Select screen [0]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			screenIdx, err = strconv.Atoi(input)
			if err != nil || screenIdx < 0 || screenIdx >= len(screens) {
				return nil, fmt.Errorf("invalid screen selection: %s", input)
			}
		}
	} else {
		fmt.Printf("  Using: %s\n", screens[0].Name)
	}

	// Pick audio device
	fmt.Println("\nAvailable audio devices:")
	for i, d := range audioDevices {
		fmt.Printf("  [%d] %s\n", i, d.Name)
	}
	fmt.Printf("Select audio device [0]: ")
	audioIdx := 0
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input != "" {
		audioIdx, err = strconv.Atoi(input)
		if err != nil || audioIdx < 0 || audioIdx >= len(audioDevices) {
			return nil, fmt.Errorf("invalid audio selection: %s", input)
		}
	}

	cfg := &UXConfig{
		Screen:       screens[screenIdx].Index,
		AudioDevice:  audioDevices[audioIdx].Index,
		WhisperModel: DefaultWhisperModelPath(),
	}

	if err := SaveConfig(cfg); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\nSaved: screen=%d (%s), audio=%d (%s)\n",
		cfg.Screen, screens[screenIdx].Name,
		cfg.AudioDevice, audioDevices[audioIdx].Name)

	return cfg, nil
}

// EnsureConfig loads config or prompts for device selection.
func EnsureConfig() (*UXConfig, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		return cfg, nil
	}
	fmt.Println("No UX recording config found. Let's set up your devices.")
	return PromptDeviceSelection()
}

// DefaultWhisperModelPath returns the default path for the whisper model.
func DefaultWhisperModelPath() string {
	return filepath.Join(ModelsDir(), "ggml-base.en.bin")
}
