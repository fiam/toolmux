# vhs tape for the README demo GIF.
# Regenerate with: scripts/record-demo.sh  (seeds a throwaway HOME first).
# Do not run `vhs docs/demo.tape` directly — it relies on the seeded state.

Output docs/assets/toolmux-demo.gif

Set Shell bash
Set FontSize 16
Set Width 1080
Set Height 660
Set Padding 24
Set Margin 36
Set MarginFill "#010409"
Set BorderRadius 12
Set WindowBar Colorful
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms

# Point the shell at the freshly built binary and the seeded throwaway HOME so
# the recording is hermetic: no real config, credentials, or network.
Hide
Type `export PATH="$PWD/bin:$PATH"` Enter
Type `export TMDEMO="$PWD/docs/.demo-home"` Enter
Type `cd "$TMDEMO"` Enter
Type `export HOME="$TMDEMO"` Enter
Type "clear" Enter
Show

Sleep 600ms

# Turn a repeatable prompt into a command: run the Slack recap workflow.
# A live spinner covers the agent's work, then the recap prints.
Type "toolmux workflow run slack-recap --input since=24h" Sleep 500ms Enter
Sleep 4s

Sleep 1500ms
