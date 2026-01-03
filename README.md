# qwebcamsrv
A quick'n'dirty HTTP server that serves a live webcam image at /img.jpg. This is designed to work with Triforce games.

Currently uses V4L2 or Windows Media Foundation directly to get a video feed, but there is a test mode that uses a specific image file.

### You can find builds [here](https://github.com/jchv/qwebcamsrv/releases/tag/nightly).

## Build

Requires Go.

```
go build -o qwebcamsrv .
```

## Usage

### Command Line Flags

```
-cropwide
   Captures a wide frame and crops it
-debug
   Enable debug messages
-device string
   Camera device ID (empty for default)
-list
   List available camera devices
-listen string
   HTTP listen address (default ":8080")
-test string
   Test mode: path to input image file
```

### Arch Linux package

There is a PKGBUILD in this directory. In order to build it, you need at least `base-devel` and `git`.

```
sudo pacman -S base-devel git
```

Build and install it with:

```
makepkg -si
```

Any other necessary dependencies will automatically be pulled in by `makepkg`.

### Systemd service

There is an included systemd service for running the server automatically at boot.

To use it, first, install the systemd files to the correct location. (They are already installed with the Arch Linux package)

Then, enable the service. You can select which webcam device to use by changing the configuration in `/etc/qwebcamsrv.conf`.

```
sudo systemctl enable --now qwebcamsrv.service
```

The server will listen on `localhost:80` by default. You can edit `qwebcamsrv.socket` to change this. (It's possible to edit installed units with `systemctl edit`.)

## License

Licensed under the ISC license. See [LICENSE](./LICENSE).
