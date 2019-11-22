package main

import (
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/temoto/gpio-cdev-go"
	"github.com/urfave/cli"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var version string
var log *zap.SugaredLogger
var debug = true
var exit = make(chan bool, 1)

func init() {
	consoleEncoderConfig := zap.NewDevelopmentEncoderConfig()
	// naive systemd detection. Drop timestamp if running under it
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("JOURNAL_STREAM") != "" {
		consoleEncoderConfig.TimeKey = ""
	}
	consoleEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleEncoder := zapcore.NewConsoleEncoder(consoleEncoderConfig)
	consoleStderr := zapcore.Lock(os.Stderr)
	_ = consoleStderr
	highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.ErrorLevel
	})
	core := zapcore.NewTee(
		zapcore.NewCore(consoleEncoder, os.Stderr, lowPriority),
		zapcore.NewCore(consoleEncoder, os.Stderr, highPriority),
	)
	logger := zap.New(core)
	if debug {
		logger = logger.WithOptions(
			zap.Development(),
			zap.AddCaller(),
			zap.AddStacktrace(highPriority),
		)
	} else {
		logger = logger.WithOptions(
			zap.AddCaller(),
		)
	}
	log = logger.Sugar()

}

type State struct {
	// this is kernel timestamp, unrelated to time.Time time
	LastPingNs   uint64
	LastInterval time.Duration
	// whether UPS pico signalled us to shut down
	LastShutdownUpdate time.Time
	LastEvent          time.Time
	UpsRunning         bool
	ShouldShutdown     bool
	RunShutdown        chan bool
	sync.RWMutex
}

var UpsState = State{
	RunShutdown: make(chan bool, 1),
}
var ShutdownUpdateInterval = time.Second * 10

func EventHandler(c *cli.Context) {
	gpioDev := c.String("gpiochip")
	clockPin := uint32(c.Uint("clock-pin"))
	pulsePin := uint32(c.Uint("pulse-pin"))
	log.Infof("opening device [%s]", gpioDev)
	gpiochip, err := gpio.Open(c.String("gpiochip"), "upspicod")
	if err != nil {
		log.Fatalf("error opening %s: %s", c.String("gpiochip"), err)
	}
	eventer, err := gpiochip.GetLineEvent(
		clockPin,
		gpio.GPIOHANDLE_REQUEST_INPUT,
		gpio.GPIOEVENT_REQUEST_FALLING_EDGE,
		"upspicod-clock-read",
	)

	if err != nil {
		log.Fatalf("error opening pin %d: %s", clockPin, err)
	}

	writeLines, err := gpiochip.OpenLines(
		gpio.GPIOHANDLE_REQUEST_OUTPUT,
		"upspicod-pulse-write",
		pulsePin,
	)
	if err != nil {
		log.Fatalf("error opening pin %d: %s", clockPin, err)
	}
	log.Info("Waiting for first pulse")
	ev, err := eventer.Wait(time.Minute)
	if err != nil {
		log.Warnf("error when waiting for event: %s", err)
	}
	// id is actually a direction of edge
	if ev.ID > 0 {
		UpsState.LastPingNs = ev.Timestamp
		log.Info("Got a pulse, starting the normal operations")
	} else {
		log.Warnf("No pulse from UPS Pico. Check if it is running and on correct pin. Trying to wait for longer")
		ev, err := eventer.Wait(time.Minute * 10)
		if err != nil {
			log.Panicf("error on reading events from pin: %s", err)
		}
		if ev.ID > 0 {
			UpsState.LastPingNs = ev.Timestamp
			log.Info("Got a pulse, starting the normal operations")
		} else {
			log.Panicf("still no pulse train, exiting")
		}
	}
	outState := int8(0)
	for {
		ev, err := eventer.Wait(time.Minute)
		if err != nil {
			log.Panicf("error while waiting for event: %T %s", err, err)
		}
		if ev.ID == 0 {
			log.Warn("No pulse from UPS pico")
		} else {
			UpsState.Lock()
			UpsState.LastInterval = time.Duration(ev.Timestamp - UpsState.LastPingNs)
			UpsState.LastPingNs = ev.Timestamp
			UpsState.LastEvent = time.Now()
			// normally pulses are every 400-500ms. Anything longer than a s means UPS Pico is shut down
			if UpsState.LastInterval < time.Second {
				UpsState.UpsRunning = true
			} else {
				UpsState.UpsRunning = false
			}
			UpsState.Unlock()

			// UPS pico wants flipped state on every falling edge
			outState = outState ^ 1
			writeLines.SetBulk(byte(outState))
			writeLines.Flush()
		}
		UpsState.Lock()
		if time.Since(UpsState.LastShutdownUpdate) > ShutdownUpdateInterval && outState == 0 {
			// line needs to be closed so it stops being an output
			writeLines.Close()

			readLines, err := gpiochip.OpenLines(
				gpio.GPIOHANDLE_REQUEST_INPUT,
				"upspico-pulse-read",
				pulsePin,
			)
			if err != nil {
				log.Fatalf("reopening pulse line %d for read failed: %s", pulsePin, err)
			}

			HandleData, err := readLines.Read()
			UpsState.LastShutdownUpdate = time.Now()

			if err != nil {
				log.Warnf("error reading line %d: %s", pulsePin, err)
			} else {
				if HandleData.Values[0] == 0 {
					UpsState.ShouldShutdown = true
					select {
					case UpsState.RunShutdown <- true:
					default:
						log.Warn("shutdown signal sent")
					}
				} else {
					UpsState.ShouldShutdown = false
				}
			}
			// and then reopen it so loop can continue
			readLines.Close()
			writeLines, err = gpiochip.OpenLines(
				gpio.GPIOHANDLE_REQUEST_OUTPUT,
				"upspicod-pulse-write",
				pulsePin,
			)
			if err != nil {
				log.Fatalf("reopening pulse line %d for write failed: %s", pulsePin, err)
			}
		}
		UpsState.Unlock()
	}

}
func ShutdownHandler(c chan bool) {
	<-c
	log.Warn("running shutdown in a minute")
	cmd := exec.Command("/sbin/shutdown", "-h", "1")
	err := cmd.Run()
	if err != nil {
		log.Errorf("error running shutdown: %s", err)
	}
	time.Sleep(time.Minute)
	exit <- true
}

func DumpStatePeriodically(interval time.Duration) {
	for {
		time.Sleep(interval)
		UpsState.RLock()
		log.Infof("Last event: %s, last interval: %s, ups running: %t",
			UpsState.LastEvent,
			UpsState.LastInterval,
			UpsState.UpsRunning,
		)
		UpsState.RUnlock()
	}
}

func main() {
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
		go EventHandler(c)
		go ShutdownHandler(UpsState.RunShutdown)
		go DumpStatePeriodically(time.Minute)
		<-exit
		log.Infof("shutting down")
		return nil

	}
	sort.Sort(cli.FlagsByName(app.Flags))
	app.Run(os.Args)
}
