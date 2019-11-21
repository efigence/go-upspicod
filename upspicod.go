package main

import (
	"os"
	"os/exec"
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
		cli.UintFlag{
			Name:  "clock-pin",
			Value: 27,
			Usage: "UPS Pico clock pin",
		},
		cli.UintFlag{
			Name:  "pulse-pin",
			Value: 22,
			Usage: "UPS Pico pulse pin",
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
		writeLines, err := gpiochip.OpenLines(
			gpio.GPIOHANDLE_REQUEST_OUTPUT,
			"upspico-write",
			uint32(c.Uint("pulse-pin")))
		if err != nil {
			log.Panicf("error opening write line %d: %s", c.Uint("pulse-pin"), err)
		}
		eventInterval := uint64(time.Now().Nanosecond())
		out := int8(0)
		pendingShutdown := false
		cnt := 0
		for {
			ev, err := eventer.Wait(time.Second * 5)
			if err != nil {
				log.Warningf("error while waiting for event: %T %s", err, err)
			}
			if ev.ID > 0 {
				cnt++
				diff := ev.Timestamp - eventInterval
				_ = diff
				eventInterval = ev.Timestamp
				out = out ^ 1
				log.Infof("interval: %f ms", float64(diff)/1000/1000)
				writeLines.SetBulk(byte(out))
				writeLines.Flush()
			} else {
				log.Info("empty event %+v", ev)
			}
			if cnt%2 == 1 || true {
				writeLines.Close()
				readLines, err := gpiochip.OpenLines(
					gpio.GPIOHANDLE_REQUEST_INPUT,
					"upspico-read",
					uint32(c.Uint("pulse-pin")),
				)
				if err != nil {
					log.Panicf("can't open pulse pin for reading: %s")
				}
				HandleData, err := readLines.Read()
				log.Warningf("%+v", HandleData.Values[0])
				if HandleData.Values[0] == 0 {
					pendingShutdown = true
				} else {
					pendingShutdown = false
				}
				if pendingShutdown {
					log.Warning("Triggering shutdown in 3s")
					time.Sleep(time.Second * 3)
					cmd := exec.Command("/sbin/shutdown", "-h", "now")
					err := cmd.Run()
					if err != nil {
						log.Errorf("error running shutdown: %s", err)
					}
				}

				readLines.Close()
				writeLines, err = gpiochip.OpenLines(
					gpio.GPIOHANDLE_REQUEST_OUTPUT,
					"upspico-write",
					uint32(c.Uint("pulse-pin")),
				)
				writeLines.SetBulk(byte(out))
				writeLines.Flush()
				if err != nil {
					log.Panicf("can't reopen pulse pin for writing: %s")
				}

			}
		}
		return nil
	}
	sort.Sort(cli.FlagsByName(app.Flags))
	app.Run(os.Args)
}
