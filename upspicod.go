package main

import (
	"os"
	"sort"
	"time"

	"github.com/op/go-logging"
	"github.com/temoto/gpio-cdev-go"
	"github.com/urfave/cli"
)

var version string
var log = logging.MustGetLogger("main")
var stdout_log_format = logging.MustStringFormatter("%{color:bold}%{time:2006-01-02T15:04:05.0000Z-07:00}%{color:reset}%{color} [%{level:.1s}] %{color:reset}%{shortpkg}[%{longfunc}] %{message}")

func main() {
	stderrBackend := logging.NewLogBackend(os.Stderr, "", 0)
	stderrFormatter := logging.NewBackendFormatter(stderrBackend, stdout_log_format)
	logging.SetBackend(stderrFormatter)
	logging.SetFormatter(stdout_log_format)
	app := cli.NewApp()
	app.Name = "upspicod"
	app.Description = "UPS Pico shutdown daemon"
	app.Version = version
	app.HideHelp = true
	app.Flags = []cli.Flag{
		cli.BoolFlag{Name: "help, h", Usage: "show help"},
		cli.StringFlag{
			Name:  "gpiochip",
			Value: "/dev/gpiochip0",
			Usage: "Device to use for GPIO",
		},
		cli.Uint64Flag{
			Name:  "clock-pin",
			Value: 27,
			Usage: "UPS Pico clock pin",
		},
		cli.UintFlag{
			Name:  "pulse-pin",
			Value: 22,
			Usage: "UPS Pico pulse pin",
		},
		cli.StringFlag{
			Name:  "shutdown-command",
			Value: "/sbin/shutdown -h now",
			Usage: "command to run when UPS requests shutdown",
		},
	}
	app.Action = func(c *cli.Context) error {
		if c.Bool("help") {
			cli.ShowAppHelp(c)
			os.Exit(1)
		}
		log.Infof("Starting %s version: %s", app.Name, version)
		gpiochip, err := gpio.Open(c.String("gpiochip"), "upspicod")
		if err != nil {
			log.Fatalf("error opening %s: %s", c.String("gpiochip"), err)
		}
		eventer, err := gpiochip.GetLineEvent(
			uint32(c.Uint("clock-pin")),
			gpio.GPIOHANDLE_REQUEST_INPUT,
			gpio.GPIOEVENT_REQUEST_FALLING_EDGE,
			"upspico-read",
		)
		if err != nil {
			log.Fatalf("err: %s", err)
		}
		for {
			ev, err := eventer.Wait(time.Second * 5)
			if err != nil {
				log.Warningf("error while waiting for event: %T %s", err, err)
			}
			if ev.ID > 0 {
				log.Infof("Got event %d", ev.ID)
			} else {
				log.Info("empty event %+v", ev)
			}
		}
		return nil
	}
	sort.Sort(cli.FlagsByName(app.Flags))
	app.Run(os.Args)
}
