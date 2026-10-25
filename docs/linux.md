# Linux permissions

## DSLogic

By default only root can open the DSLogic. Add a udev rule for its USB
vendor ID, 2A0E:

```sh
echo 'SUBSYSTEM=="usb", ATTRS{idVendor}=="2a0e", MODE="0666"' | \
  sudo tee /etc/udev/rules.d/60-dslogic.rules
sudo udevadm control --reload
sudo udevadm trigger
```

Then unplug the DSLogic and plug it in again. The rule also covers the
device after the tools load its firmware, because the USB ID stays the
same. Without the rule, the tools fail with `libusb: bad access`.

## BitScope

The BitScope is a USB serial port. Add your user to the group that owns
serial ports, `dialout` on Debian and Ubuntu or `uucp` on Arch, then log
in again:

```sh
sudo usermod -aG dialout $USER
```
