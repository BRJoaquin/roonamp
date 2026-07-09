# VHS tape for the README demo gif.
# Record with: vhs demo.tape   (needs a reachable Roon Core with music playing)
# https://github.com/charmbracelet/vhs

Output demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1100
Set Height 620
Set Padding 16
Set TypingSpeed 120ms

# Launch (startup connect/register output hidden)
Hide
Type "./roonamp"
Enter
Sleep 4s
Show

# -- Player view --
Sleep 3s

# Volume up, then back down (auto-hiding volume indicator + pulse animation)
Type "+"
Sleep 300ms
Type "+"
Sleep 2s
Type "-"
Sleep 300ms
Type "-"
Sleep 2s

# Zone switching (swipe animation), and back
Type ">"
Sleep 2s
Type "<"
Sleep 2s

# -- Library browser --
Type "b"
Sleep 2.5s
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms
Type "k"
Sleep 400ms
Type "k"
Sleep 800ms

# Drill into the first entry (Library on stock installs)
Type "l"
Sleep 2.5s

# Global search
Type "s"
Sleep 500ms
Type "another brick in the wall"
Sleep 800ms
Enter
Sleep 3.5s

# Fuzzy-filter the result buckets down to Tracks and drill in.
# Generous sleeps: each drill is a live Roon browse+load round trip, and a
# keypress that lands mid-load is applied to the stale list.
Type "/"
Sleep 500ms
Type "tracks"
Sleep 1.2s
Enter
Sleep 1s
Type "l"
Sleep 4s

# Open the top track's action menu, then Play Now (returns to the player)
Type "l"
Sleep 4s
Type "l"
Sleep 10s

# -- Lyrics view (synced, follows the playing track) --
Type "L"
Sleep 16s
Type "q"
Sleep 3s
