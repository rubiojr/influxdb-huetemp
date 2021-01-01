// Needs HUE_API_KEY, HUE_BRIDGE_IP, INFLUXDB_TOKEN, INFLUXDB_URL, INFLUXDB_ORG,
// and INFLUXDB_BUCKET environment variables exported.

// Get the bridge IP with: curl https://discovery.meethue.com/
//
// Press the Hue bridge button and:
//   curl -d '{"devicetype":"huetemp"}' --header "Content-Type: application/json" --request POST http://bridge-ip/api
//
// to get an API key.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	influxdb "github.com/influxdata/influxdb-client-go/v2"
	log "github.com/sirupsen/logrus"
)

const INTERVAL = 5 * time.Minute

var idToName = map[string]string{}

// HueBridges holds the hue bridge objects
type HueBridges []HueBridge

// HueBridge structure for discovered hue bridges
type HueBridge struct {
	ID                string `json:"id"`
	Internalipaddress string `json:"internalipaddress"`
}

// HueResources storing hue resources
type HueResources struct {
	Config        map[string]interface{} `json:"config"`
	Scenes        map[string]interface{} `json:"scenes"`
	Schedules     map[string]interface{} `json:"schedules"`
	Sensors       map[string]HueSensor   `json:"sensors"`
	Resourcelinks map[string]interface{} `json:"resourcelinks"`
	Lights        map[string]interface{} `json:"lights"`
	Rules         map[string]interface{} `json:"rules"`
}

// HueSensor storing hue sensor objects
type HueSensor struct {
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    HueSensorConfig `json:"config"`
	State     HueSensorState  `json:"state"`
	UniqueID  string          `json:"uniqueid"`
	SWVersion string          `json:"swversion"`
}

// HueSensorState storing hue sensor state
type HueSensorState struct {
	Temperature float64 `json:"temperature"`
	Lightlevel  float64 `json:"lightlevel"`
}

// HueSensorConfig storing hue sensor config
type HueSensorConfig struct {
	Battery float64 `json:"battery"`
}

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	log.SetLevel(log.DebugLevel)
}

func (s *HueSensor) CommonID() string {
	return s.UniqueID[0:23]
}

func (s *HueSensor) FixedName() string {
	fixedName := idToName[s.CommonID()]
	return strings.ReplaceAll(fixedName, " ", "_")
}

func discoverHueSensors(hueBridges HueBridges, hueAPIKey string) error {
	for _, value := range hueBridges {
		bridgeAddress := value.Internalipaddress
		hueSensorURL := "http://" + bridgeAddress + "/api/" + hueAPIKey
		response, err := http.Get(hueSensorURL)
		if err != nil {
			return err
		}
		defer response.Body.Close()

		responseData, err := ioutil.ReadAll(response.Body)
		if err != nil {
			return err
		}

		var hueResources HueResources
		err = json.Unmarshal([]byte(responseData), &hueResources)
		if err != nil {
			return err
		}
		hueSensors := hueResources.Sensors

		var tupdates, lupdates []HueSensor

		for _, Value := range hueSensors {
			hueSensor := Value
			if hueSensor.Type == "ZLLPresence" {
				cid := hueSensor.CommonID()
				idToName[cid] = hueSensor.Name
			}
			if hueSensor.Type == "ZLLTemperature" {
				tupdates = append(tupdates, hueSensor)
			}
			if hueSensor.Type == "ZLLLightLevel" {
				lupdates = append(lupdates, hueSensor)
			}
		}

		for _, hueSensor := range tupdates {
			hueSensor.State.Temperature = hueSensor.State.Temperature / 100
			payload := "hue," + "name=" + fmt.Sprint(hueSensor.FixedName()) + " temperature=" + fmt.Sprint(hueSensor.State.Temperature) + ",battery=" + fmt.Sprint(hueSensor.Config.Battery)
			log.Debug(payload)
			postToInflux(payload)
		}

		for _, hueSensor := range lupdates {
			lux := hueSensor.State.Lightlevel - 1
			lux = lux / 10000
			lux = math.Pow(10, lux)
			payload := "hue," + "name=" + fmt.Sprint(hueSensor.FixedName()) + " lux=" + fmt.Sprint(lux) + ",battery=" + fmt.Sprint(hueSensor.Config.Battery)
			log.Debug(payload)
			postToInflux(payload)
		}

	}
	return nil
}

func postToInflux(payload string) {

	client := influxdb.NewClient(env()["INFLUXDB_URL"], env()["INFLUXDB_TOKEN"])
	defer client.Close()

	writeAPI := client.WriteAPI(env()["INFLUXDB_ORG"], env()["INFLUXDB_BUCKET"])
	writeAPI.WriteRecord(payload)
	writeAPI.Flush()
}

func env() map[string]string {
	env := map[string]string{}
	env["INFLUXDB_TOKEN"] = os.Getenv("INFLUXDB_TOKEN")
	env["INFLUXDB_BUCKET"] = os.Getenv("INFLUXDB_BUCKET")
	env["INFLUXDB_ORG"] = os.Getenv("INFLUXDB_ORG")
	env["INFLUXDB_URL"] = os.Getenv("INFLUXDB_URL")
	env["HUE_API_KEY"] = os.Getenv("HUE_API_KEY")
	env["HUE_BRIDGE_IP"] = os.Getenv("HUE_BRIDGE_IP")

	return env
}

func main() {
	for k, v := range env() {
		if v == "" {
			log.Fatalf("missing env var %s", k)
		}
	}

	hueBridges := []HueBridge{HueBridge{ID: "mybridge", Internalipaddress: env()["HUE_BRIDGE_IP"]}}
	err := discoverHueSensors(hueBridges, env()["HUE_API_KEY"])
	if err != nil {
		log.Debug(err)
	}

	// Scheduled scan
	tick := time.Tick(INTERVAL)
	for range tick {
		err = discoverHueSensors(hueBridges, env()["HUE_API_KEY"])
		if err != nil {
			log.Debug(err)
		}
	}
}
