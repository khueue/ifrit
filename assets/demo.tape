# VHS Demo: ifrit CLI
# Run: vhs assets/demo.tape

Output assets/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms
Set CursorBlink false

Sleep 500ms

# Show the config
Type "cat ifrit.yml"
Sleep 300ms
Enter
Sleep 2s

# Start all projects
Type "ifrit up"
Sleep 300ms
Enter
Sleep 3s

# Peek at logs
Type "ifrit logs"
Sleep 300ms
Enter
Sleep 1s
Type "2"
Sleep 2s
Type "q"
Sleep 1s

# Check status
Type "ifrit status"
Sleep 300ms
Enter
Sleep 3s

# Check status
Type "ifrit shell proj1 dev"
Sleep 300ms
Enter
Sleep 2s
Type "curl http://ifrit_proj2-app-1/"
Sleep 300ms
Enter
Sleep 2s
Type "exit"
Sleep 300ms
Enter
Sleep 1s

# Stop everything
Type "ifrit down"
Sleep 300ms
Enter
Sleep 1s

# Fin
Sleep 2s
