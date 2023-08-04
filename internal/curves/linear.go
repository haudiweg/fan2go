package curves

import (
	"math"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type LinearSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`
}

func (c *LinearSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *LinearSpeedCurve) Evaluate() (value int, err error) {
	var avgTemp float64
	if c.Config.Linear.Sensor != "" {
		sensor := sensors.SensorMap[c.Config.Linear.Sensor]
		avgTemp = sensor.GetMovingAvg()
	} else if c.Config.Linear.Curve != "" {
		v, err := SpeedCurveMap[c.Config.Linear.Curve].Evaluate()
		if err != nil {
			ui.Debug("%s invalid", c.GetId())
			return 0, err
		}
		//avgTemp = math.Min(float64(v), 255)
		//avgTemp = (float64(v) / 255) * 100 * 1000
		avgTemp = float64(v) * 1000
		//ui.Debug("%-45s avgTemp %7.4f", c.GetId(), avgTemp)
	}

	steps := c.Config.Linear.Steps
	if steps != nil {
		value = int(math.Round(util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)))
	} else {
		minTemp := float64(c.Config.Linear.Min) * 1000 // degree to milli-degree
		maxTemp := float64(c.Config.Linear.Max) * 1000

		if avgTemp >= maxTemp {
			// full throttle if max temp is reached
			value = 255
		} else if avgTemp <= minTemp {
			// turn fan off if at/below min temp
			value = 0
		} else {
			ratio := (avgTemp - minTemp) / (maxTemp - minTemp)
			value = int(ratio * 255)
		}
	}

	c.Value = value
	return value, nil
}
