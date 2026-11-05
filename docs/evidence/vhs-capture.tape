# vhs-capture.tape: a before/after capture template for prstk audit evidence, for
# terminal output, CLIs and TUIs. (For anything in a browser use playwright-capture.mjs.)
#
# Run it once before you change any code, and again on the approved code. To get the
# "after" tape from the "before" one, change every "before" to "after" in the file names:
#
#   sed 's/before/after/g' before.tape > after.tape
#
# Then:  vhs before.tape      (or vhs after.tape)
#
# Everything marked EDIT is yours to change. Keep the Set lines identical between the
# two runs, so the only difference between before and after is the code under test.

# EDIT: the video for this phase. vhs picks the format from the extension. Use before.webm
# for the "before" run and after.webm for the "after" run.
Output before.webm

# Fixed look. Do not vary these between the before and after runs.
Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 700
Set Padding 20
Set Theme "Dracula"
Set TypingSpeed 40ms
Set Framerate 30

# Fixed environment so output is the same on every run (no colour surprises, no locale
# or timezone differences, no timestamps from the prompt).
Env TZ "UTC"
Env LANG "en_US.UTF-8"
Env NO_COLOR "1"

# Setup, kept out of the recording. EDIT: cd to wherever the app is built, and build it
# here if it is not built already. For a Go CLI built from the worktree, for example:
#   Type "cd /path/to/worktree && go build -o /tmp/app ./cmd/app" Enter
Hide
Type "export PS1='$ '" Enter
Type "cd /path/to/worktree" Enter
Type "clear" Enter
Show

# Step 1. EDIT: the command to run, and how long it needs to finish.
Type "./app --help" Enter
Sleep 2s
Screenshot before-step-1.png

# Step 2. EDIT: the next state to reach. Delete or copy this block for more steps; the
# step number stays in the file name so it matches `prstk evidence add ... --step 2`.
Type "./app list --limit 3" Enter
Sleep 2s
Screenshot before-step-2.png

# A last pause so the video does not end mid-frame.
Sleep 1s
