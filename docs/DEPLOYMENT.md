# User service

Build the native binary:

```bash
./scripts/build.sh
```

Install, enable and start the user service:

```bash
./scripts/start.sh
```

The start script copies `deploy/newspaperbot.service` to
`~/.config/systemd/user/newspaperbot.service`, reloads the user manager, enables
the unit for future logins and reboots, and starts it. User lingering must remain
enabled for startup before login. Repeated runs copy the same unit and `enable
--now` leaves an already-running service alone.

The service runs `newspaperbot` directly from this repository, uses the repository as
its working directory, and loads `.env` directly. SQLite therefore remains at the
configured `DB_PATH`, and the cached portfolio checkout remains at the configured
`PORTFOLIO_CACHE_DIR`. Its explicit PATH selects Node 22 from the existing NVM
installation because the portfolio requires Node 22 and the user manager's default
PATH selects Node 20. The unit uses systemd's `%h` specifier instead of hardcoding
the username or home directory.

`After=network.target telegram-bot-api.service` is included as requested. The Bot
API currently runs in the system manager, while this bot runs in the user manager;
systemd cannot enforce ordering across those two managers. `Restart=on-failure`
retries the bot if it exits because the API or network is not ready.

View logs with:

```bash
journalctl --user -u newspaperbot.service -f
```

After rebuilding the binary, apply it to an already-running service with
`systemctl --user restart newspaperbot.service`.
