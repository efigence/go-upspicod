# upspicod

Daemon for generating pulse train and reading shutdown info for [UPS Pico](https://github.com/modmypi/PiModules/wiki).



##Requirements:

* `/dev/gpiochip` support in kernel (4.8+).

## Setup

`go get efigence/go-upspicod` 

or navigate to the root of directory and type `make`

Then just run it. Daemon uses default pins and device for rPi 3 B+, if you need to change device or pin refer to `--help`

