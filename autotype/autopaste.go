package autotype

import (
	"fmt"
	"time"

	"github.com/bendahl/uinput"
)

// PressPaste simulates the paste key being pressed by using a fake /dev/uinput device.
// Tested on Wayland and working, your mileage may vary on other systems.
func PressPaste() error {
	keyboard, err := uinput.CreateKeyboard("/dev/uinput", []byte("gopasspasteplugin"))
	if err != nil {
		return fmt.Errorf("create virtual keyboard: %w", err)
	}
	defer keyboard.Close()

	if err := keyboard.KeyPress(uinput.KeyPaste); err != nil {
		return fmt.Errorf("key press for paste: %w", err)
	}

	// Give events time to be processed before destroying the device
	time.Sleep(100 * time.Millisecond)
	return nil
}
