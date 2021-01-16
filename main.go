/*
 * fan2go
 * Copyright (c) 2019. Markus Ressel
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at ydour option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */
package main

import (
	"errors"
	"fan2go/config"
	"fmt"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	bolt "go.etcd.io/bbolt"
	//"github.com/markusressel/fan2go/cmd"
	"log"
	"os"
	"path/filepath"
)

const (
	MaxPwmValue   = 255
	BucketFans    = "fans"
	BucketSensors = "sensors"
)

type Controller struct {
	name     string
	dType    string
	modalias string
	platform string
	path     string
	fans     []*Fan
	sensors  []*Sensor
}

type Fan struct {
	name      string
	index     int
	rpmInput  string // RPM values
	pwmOutput string // PWM control
	config    *config.FanConfig
}

type Sensor struct {
	name  string
	index int
	input string
}

var (
	Controllers []Controller
	Database    *bolt.DB
)

func main() {
	// TODO: enable
	//if getProcessOwner() != "root" {
	//	log.Fatalf("Please run fan2go as root")
	//}

	// TODO: cmd line parameters
	//cmd.Execute()

	DB, err := bolt.Open("fan2go.db", 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		log.Fatal(err)
	}
	Database = DB
	defer Database.Close()

	Controllers, err = findControllers()
	if err != nil {
		log.Fatalf("Error detecting devices: %s", err.Error())
	}

	for _, controller := range Controllers {
		for _, fan := range controller.fans {
			fanConfig := findFanConfig(controller, *fan)
			if fanConfig != nil {
				fan.config = fanConfig
			}
		}
		// TODO: match sensors and sensor config entries
	}

	// TODO: measure fan curves / use realtime measurements to update the curve?
	// TODO: save reference fan curves in db

	go monitor()
	go fanSpeedUpdater()

	// wait forever
	select {}
}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}

// Finds controllers and fans
func findControllers() (controllers []Controller, err error) {
	hwmonDevices := findHwmonDevicePaths()
	i2cDevices := findI2cDevicePaths()
	allDevices := append(hwmonDevices, i2cDevices...)

	platformRegex := regexp.MustCompile(".*/platform/{}/.*")

	for _, devicePath := range allDevices {
		name := getDeviceName(devicePath)
		dType := getDeviceType(devicePath)
		modalias := getDeviceModalias(devicePath)
		platform := platformRegex.FindString(devicePath)
		if len(platform) <= 0 {
			platform = name
		}

		fans := createFans(devicePath)
		sensors := createSensors(devicePath)

		if len(fans) <= 0 {
			//log.Printf("No usable PWM outputs for %s, skipping.", devicePath)
			continue
		}

		controller := Controller{
			name:     name,
			dType:    dType,
			modalias: modalias,
			platform: platform,
			path:     devicePath,
			fans:     fans,
			sensors:  sensors,
		}
		controllers = append(controllers, controller)
	}

	return controllers, err
}

func findI2cDevicePaths() []string {
	basePath := "/sys/bus/i2c/devices"

	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file does not exist
		} else {
			// other error
		}
		return []string{}
	}

	return findFilesMatching(basePath, ".+-.+")

	//	# Find available fan control outputs
	//	MATCH=$device/'pwm[1-9]'
	//	device_pwm=$(echo $MATCH)
	//	if [ "$SYSFS" = "1" -a "$MATCH" = "$device_pwm" ]
	//	then
	//		# Deprecated naming scheme (used in kernels 2.6.5 to 2.6.9)
	//		MATCH=$device/'fan[1-9]_pwm'
	//		device_pwm=$(echo $MATCH)
	//	fi
	//	if [ "$MATCH" != "$device_pwm" ]
	//	then
	//		PWM="$PWM $device_pwm"
	//	fi
}

func findHwmonDevicePaths() []string {
	basePath := "/sys/class/hwmon"
	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file does not exist
		} else {
			// other error
		}
		return []string{}
	}

	result := findFilesMatching(basePath, "hwmon.*")

	return result
}

// goroutine to monitor temp and fan sensors
func monitor() {
	for _, device := range Controllers {
		for _, sensor := range device.sensors {
			watcher, err := startSensorWatcher(*sensor)
			if err != nil {
				log.Print(err.Error())
			} else {
				defer watcher.Close()
			}
		}
		for _, fan := range device.fans {
			watcher, err := startFanFsWatcher(*fan)
			if err != nil {
				log.Print(err.Error())
			} else {
				defer watcher.Close()
			}

		}
	}

	//t := time.Tick(2 * time.Second)
	//for {
	//	select {
	//	case <-t:
	//		//updateInputs()
	//		printDeviceStatus(Controllers)
	//	}
	//}
}

func startFanFsWatcher(fan Fan) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					err := updateFan(fan)
					if err != nil {
						log.Print(err.Error())
					}
					key := fmt.Sprintf("%s_pwm", fan.name)
					newValue, _ := readInt(BucketFans, key)
					log.Printf("%s PWM: %d", fan.name, newValue)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	err = watcher.Add(fan.rpmInput)
	err = watcher.Add(fan.pwmOutput)
	if err != nil {
		log.Fatal(err.Error())
	}

	return watcher, err
}

func updateFan(fan Fan) (err error) {
	pwmValue := getPwm(fan)
	rpmValue, err := readIntFromFile(fan.rpmInput)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s_pwm", fan.name)
	err = storeInt(BucketFans, key, pwmValue)
	if err != nil {
		return err
	}
	key = fmt.Sprintf("%s_rpm", fan.name)
	err = storeInt(BucketFans, key, rpmValue)
	return err
}

func startSensorWatcher(sensor Sensor) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					err := updateSensor(sensor)
					if err != nil {
						log.Print(err.Error())
					}
					newValue, _ := readInt(BucketSensors, sensor.input)
					log.Printf("%s: %d", sensor.name, newValue)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	err = watcher.Add(sensor.input)
	if err != nil {
		log.Fatal(err.Error())
	}

	return watcher, err
}

func updateInputs() {
	for _, device := range Controllers {
		for _, sensor := range device.sensors {
			err := updateSensor(*sensor)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func updateSensor(sensor Sensor) (err error) {
	value, err := readIntFromFile(sensor.input)
	if err != nil {
		return err
	}
	return storeInt(BucketSensors, sensor.input, value)
}

// goroutine to update fan speeds
func fanSpeedUpdater() {
	t := time.Tick(5 * time.Second)
	for {
		select {
		case <-t:
			log.Printf("Updating fan speeds...")
			for _, controller := range Controllers {
				for _, fan := range controller.fans {
					if fan.config == nil {
						continue
					}
					err := setPwmEnabled(*fan, 1)
					if err != nil {
						err = setPwmEnabled(*fan, 0)
						if err != nil {
							log.Printf("Could not enable fan control on %s", fan.name)
							continue
						}
					}
					setOptimalFanSpeed(*fan)
				}
			}
		}
	}
}

func findFanConfig(controller Controller, fan Fan) (fanConfig *config.FanConfig) {
	for _, fanConfig := range config.CurrentConfig.Fans {
		if controller.platform == fanConfig.Platform &&
			fan.index == fanConfig.Fan {
			return &fanConfig
		}
	}
	return nil
}

// calculates optimal fan speeds for all given devices
func setOptimalFanSpeed(fan Fan) {
	target := calculateTargetSpeed(fan)
	err := setPwm(fan, target)
	if err != nil {
		log.Printf("Error setting %s/%d: %s", fan.name, fan.index, err.Error())
	}
}

// calculates the target speed for a given device output
func calculateTargetSpeed(fan Fan) int {
	// TODO: calculate target fan curve based on min/max temp values

	pwm := getPwm(fan)
	if pwm < 255 {
		return 255
	}

	return 1
	//return rand.Intn(getMaxPwmValue(fan))
}

func printDeviceStatus(devices []Controller) {
	for _, device := range devices {
		log.Printf("Controller: %s", device.name)
		for _, fan := range device.fans {
			value := getPwm(*fan)
			isAuto, _ := isPwmAuto(device.path)
			log.Printf("Output: %s Value: %d Auto: %v", fan.name, value, isAuto)
		}

		for _, sensor := range device.sensors {
			value, _ := readIntFromFile(sensor.input)
			log.Printf("Input: %s Value: %d", sensor.name, value)
		}
	}
}

func getDeviceName(devicePath string) string {
	namePath := devicePath + "/name"
	content, _ := ioutil.ReadFile(namePath)
	name := string(content)
	if len(name) <= 0 {
		_, name = filepath.Split(devicePath)
	}
	return strings.TrimSpace(name)
}

func getDeviceModalias(devicePath string) string {
	modaliasPath := devicePath + "/device/modalias"
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

func getDeviceType(devicePath string) string {
	modaliasPath := devicePath + "/device/type"
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

func findFilesMatching(path string, regex string) []string {
	r, err := regexp.Compile(regex)
	if err != nil {
		log.Fatalf("Cannot compile regex: %s", regex)
	}

	var result []string
	err = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf(err.Error())
		}

		if !info.IsDir() && r.MatchString(info.Name()) {
			var devicePath string

			// we may need to adjust the path (pwmconfig cite...)
			_, err := os.Stat(path + "/name")
			if os.IsNotExist(err) {
				devicePath = path + "/device"
			} else {
				devicePath = path
			}

			devicePath, err = filepath.EvalSymlinks(devicePath)
			if err != nil {
				panic(err)
			}

			//fmt.Printf("File Name: %s\n", info.Name())
			result = append(result, devicePath)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}

func createFans(path string) []*Fan {
	var fans []*Fan

	inputs := findFilesMatching(path, "^fan[1-9]_input$")
	outputs := findFilesMatching(path, "^pwm[1-9]$")

	for idx, output := range outputs {
		_, file := filepath.Split(output)

		index, err := strconv.Atoi(file[len(file)-1:])
		if err != nil {
			log.Fatal(err)
		}

		fans = append(fans, &Fan{
			name:      file,
			index:     index,
			pwmOutput: output,
			rpmInput:  inputs[idx],
		})
	}

	return fans
}

func createSensors(path string) []*Sensor {
	var sensors []*Sensor

	inputs := findFilesMatching(path, "^temp[1-9]_input$")

	for _, input := range inputs {
		_, file := filepath.Split(input)

		index, err := strconv.Atoi(string(file[4]))
		if err != nil {
			log.Fatal(err)
		}

		sensors = append(sensors, &Sensor{
			name:  file,
			index: index,
			input: input,
		})
	}

	return sensors
}

// checks if the given output is in auto mode
func isPwmAuto(outputPath string) (bool, error) {
	pwmEnabledFilePath := outputPath + "_enable"

	if _, err := os.Stat(pwmEnabledFilePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		panic(err)
	}

	value, err := readIntFromFile(pwmEnabledFilePath)
	if err != nil {
		return false, err
	}
	return value > 1, nil
}

// Writes the given value to pwmX_enable
// Possible values (unsure if these are true for all scenarios):
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
func setPwmEnabled(fan Fan, value int) (err error) {
	pwmEnabledFilePath := fan.pwmOutput + "_enable"
	err = writeIntToFile(value, pwmEnabledFilePath)
	if err == nil {
		value, err := readIntFromFile(pwmEnabledFilePath)
		if err != nil || value != value {
			return errors.New(fmt.Sprintf("PWM mode stuck to %d", value))
		}
	}
	return err
}

func getPwmEnabled(outputPath string) (int, error) {
	pwmEnabledFilePath := outputPath + "_enable"
	return readIntFromFile(pwmEnabledFilePath)
}

//func setAllFandsToMax(outputPath string) (err error) {
//	pwmEnabledFilePath := outputPath + "_enable"
//
//	// I think this tries to set all fans to maximum speed to
//	// detect which fans are actually populated.
//
//	if _, err := os.Stat(pwmEnabledFilePath); err != nil {
//		if os.IsNotExist(err) {
//			// No enable file? Just set to max
//			err = writeIntToFile(MaxPwmValue, pwmEnabledFilePath)
//			return err
//		}
//		panic(err)
//	}
//
//	// Try pwmN_enable=0
//	//err = writeIntToFile(0, pwmEnabledFilePath)
//	//if err == nil {
//	//	value := readIntFromFile(pwmEnabledFilePath)
//	//	if value == 0 {
//	//		// success
//	//		return err
//	//	}
//	//}
//
//	//	# It didn't work, try pwmN_enable=1 pwmN=255
//	err = setPwmEnabled(outputPath, 1)
//	if err != nil {
//		return err
//	}
//
//	err = writeIntToFile(getMaxPwmValue(outputPath), outputPath)
//	if err == nil {
//		time.Sleep(1 * time.Second)
//		value := readIntFromFile(outputPath)
//		if value >= getMaxPwmValue(outputPath) {
//			// success
//			return nil
//		} else {
//			return errors.New(fmt.Sprintf("PWM stuck to %d", value))
//		}
//	}
//
//	return err
//}

func getMaxPwmValue(fan Fan) (result int) {
	err := Database.View(func(tx *bolt.Tx) error {
		key := fmt.Sprintf("%s_pwm_max", fan.name)
		value, err := readInt(BucketFans, key)
		if err == nil {
			result = value
		}
		return err
	})

	if err == nil {
		return result
	} else {
		log.Print(err.Error())
		result = MaxPwmValue
	}

	return result
}

// get the pwm speed of a fan (0..255)
func getPwm(fan Fan) int {
	value, err := readIntFromFile(fan.pwmOutput)
	if err != nil {
		return 0
	}
	return value
}

// set the pwm speed of a fan to the specified value (0..255)
func setPwm(fan Fan, pwm int) (err error) {
	if pwm > 255 {
		pwm = 255
	} else if pwm < 0 {
		pwm = 0
	}

	log.Printf("Setting %s to %d ...", fan.name, pwm)
	return writeIntToFile(pwm, fan.pwmOutput)
}

// ===== Bolt =====
func readInt(bucket string, key string) (result int, err error) {
	err = Database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		result, err = strconv.Atoi(string(b.Get([]byte(key))))
		return err
	})
	return result, err
}

func storeInt(bucket string, key string, value int) (err error) {
	return Database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), []byte(strconv.Itoa(value)))
		return nil
	})
}

// ===== File Access =====

func readIntFromFile(path string) (value int, err error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Println("File reading error", err)
		return -1, nil
	}
	text := string(data)
	text = strings.TrimSpace(text)
	return strconv.Atoi(text)
}

// write a single integer to a file path
func writeIntToFile(value int, path string) (err error) {
	f, err := os.OpenFile(path, os.O_SYNC|os.O_WRONLY, 644)
	if err != nil {
		return err
	}
	defer f.Close()

	valueAsString := fmt.Sprintf("%d", value)
	_, err = f.WriteString(valueAsString)
	return err
}
