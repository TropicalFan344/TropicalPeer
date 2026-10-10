# Keeps a DuckDNS name pointed at this machine's public address, for a
# home-hosted rendezvous server whose address changes.
#
#   1. Copy this file to duckdns.ps1 next to it (git ignores that name, so your token stays private).
#   2. Fill in your subdomain and the token from https://www.duckdns.org.
#   3. Run it every few minutes with Task Scheduler.
$domain = "your-name"     # just the part before .duckdns.org
$token  = "your-token"
$r = Invoke-RestMethod "https://www.duckdns.org/update?domains=$domain&token=$token&ip="
"$(Get-Date -Format s)  $r" | Out-File "$env:USERPROFILE\duckdns.log" -Encoding utf8
