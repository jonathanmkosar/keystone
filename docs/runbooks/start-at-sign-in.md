# Starting Keystone at sign-in on Windows

`tools/windows/start-keystone.ps1` starts Keystone in the background with no window, unless something already listens on its address. It runs this checkout's `bin\keystone.exe` with the checkout as its folder, so the data folder is the checkout's `data\`, and writes the server's output to `%LOCALAPPDATA%\Keystone\logs`, keeping the newest ten runs.

## Set it up

Make a shortcut in the Startup folder (`shell:startup`) that runs:

    powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "<checkout>\tools\windows\start-keystone.ps1" -Address 127.0.0.1:9080

with the checkout as its folder. The address is the one the tunnel sends the domain to; without `-Address` the script uses Keystone's own default, `127.0.0.1:7900`. Windows runs it each time you sign in.

## Release a new build

Stop the running server, build, and start it again with the same script:

    Get-NetTCPConnection -State Listen -LocalPort 9080 | ForEach-Object { Stop-Process -Id $_.OwningProcess }
    go build -o bin\keystone.exe ./cmd/keystone
    powershell -NoProfile -ExecutionPolicy Bypass -File tools\windows\start-keystone.ps1 -Address 127.0.0.1:9080

A restore of a tenant (`docs/runbooks/backup-and-restore.md`) is done between the first and the last step, with the server stopped.

## Turn it off

Delete the shortcut from the Startup folder.
