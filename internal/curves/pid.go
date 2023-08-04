package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type PidSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`

	pidLoop *util.PidLoop
}

func (c *PidSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *PidSpeedCurve) Evaluate() (value int, err error) {
	var measured float64
	if c.Config.PID.Sensor != "" {
		sensor := sensors.SensorMap[c.Config.PID.Sensor]
		measured, err = sensor.GetValue()
		if err != nil {
			return c.Value, err
		}
	} else if c.Config.PID.Curve != "" {
		v, err := SpeedCurveMap[c.Config.PID.Curve].Evaluate()
		if err != nil {
			return 0, err
		}
		measured = float64(v) * 1000
	} else {
		ui.Fatal("no imput selectet use Sensor or Curve")
	}

	pidTarget := c.Config.PID.SetPoint

	loopValue := c.pidLoop.Loop(pidTarget, measured/1000.0)

	// clamp to (0..1)
	if loopValue > 1 {
		loopValue = 1
	} else if loopValue < 0 {
		loopValue = 0
	}

	// map to expected output range
	curveValue := int(loopValue * 255)

	c.Value = curveValue
	return curveValue, nil
}
