# cosmic-launcher plugin for gopass

This is meant to let you use the default cosmic-launcher on Cosmic to copy your passwords.

Simply clone this repo and then compile the binary with `go build` before moving the `gopass.ron` and the binary in `~/.local/share/pop-launcher/plugins/gopass`

For example, this should work:
```
go build -o plugins/gopass/cosmic-gopass-plugin . 
rm -rf ~/.local/share/pop-launcher/plugins/gopass 
cp -r plugins/gopass ~/.local/share/pop-launcher/plugins/gopass
```

And then just try typing `gp ` in cosmic-launcher to see your gopass entries.

## Usage

Type `gp` followed by words to search for. An entry matches when it contains every word, in any order, ignoring case. Activating an entry copies its password to the clipboard and pastes it.

To copy something else, add one of these options anywhere in the query:

| Option | Copies |
| --- | --- |
| `:KEY`, for example `:user` or `:url` | The `KEY` field of the entry (`gopass show -c ENTRY KEY`) |
| `-o`, `-otp` | The current OTP code (`gopass otp --clip ENTRY`) |
| `-n`, `-name` | The last path component of the entry name, using `wl-copy` |

For example, `gp github work :user` copies the user of `work/github.com`.

# Important dev details

This isn't very well documented in github.com/pop-os/launcher at the moment, but all the received `Search` queries on stdin need a `"Finished"` response, even when a new `Search` or a new `Interrupt` arrives to cancel the previous one. 
