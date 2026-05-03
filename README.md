[![Go Report Card](https://goreportcard.com/badge/github.com/MonsieurTib/service-bus-tui)](https://goreportcard.com/report/github.com/MonsieurTib/service-bus-tui)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=MonsieurTib_service-bus-tui&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=MonsieurTib_service-bus-tui)
[![CI](https://github.com/MonsieurTib/service-bus-tui/actions/workflows/release.yml/badge.svg)](https://github.com/MonsieurTib/service-bus-tui/actions/workflows/release.yml)

[![GitHub Release](https://img.shields.io/github/v/release/MonsieurTib/service-bus-tui)](https://github.com/MonsieurTib/service-bus-tui/releases)
![Homebrew](https://img.shields.io/github/v/release/MonsieurTib/service-bus-tui?label=homebrew)
[![WinGet Version](https://img.shields.io/winget/v/MonsieurTib.service-bus-tui)](https://winstall.app/apps/MonsieurTib.service-bus-tui)
[![Chocolatey](https://img.shields.io/chocolatey/v/service-bus-tui)](https://community.chocolatey.org/packages/service-bus-tui)

# service-bus-tui

A terminal-based explorer for Azure Service Bus.

> **Work in Progress**: This project is currently under active development.

## Previews

<img width="2097" height="1207" alt="asb-tui" src="https://github.com/user-attachments/assets/ed282136-e839-46cc-bd6a-51a274e9372f" />

<img width="2155" height="1234" alt="send-message" src="https://github.com/user-attachments/assets/46610c48-3dde-4142-a781-490e1379ef0b" />


## Features

### Authentication
- Azure CLI authentication (uses existing `az login` session)
- Interactive browser authentication
- Service principal (client ID / client secret)
- Connection string
- Optional save prompt after connection-string login
- Saved connection strings list with quick connect and delete
- Emulator (one-click connect to local Service Bus emulator)

### Namespace Discovery
- Automatically lists all Service Bus namespaces across your Azure subscriptions
- Displays namespace name, subscription ID, and resource group

### Resource Browsing
- Tree-based navigation of namespaces
- List topics and queues
- Expand topics to view subscriptions
- Expand queues to view active and DLQ messages
- View active messages and dead-letter queue (DLQ) messages per subscription or queue

### Message Viewing
- Peek messages from subscriptions and queues (active and DLQ)
- Tabular display with sequence number, message ID, subject, enqueued time, and body preview
- JSON body formatting in preview
- Paginated browsing (100 messages per page) with automatic page detection at boundaries
- Copy raw message body to clipboard from message list/detail (`ctrl+y`)

### Message Resending
- Resend selected messages back to their source topic or queue
- If nothing is selected, resend uses the currently highlighted row
- Single-message resend opens editable send form prefilled with original message body/metadata
- Works on both active and dead-letter messages (DLQ replay)
- Multi-message selection support
- Option to keep original or generate new Message IDs
- Real-time progress tracking

### Message Sending
- Send new messages directly to a topic or queue
- Compose subject, content type, and body from an in-app modal form
- Content type selection with built-in presets and custom value support
- Copy compose/resend-edit body to clipboard (`ctrl+y`)

### Navigation
- Keyboard-driven interface
- `up/down` or `j/k`: Navigate items
- `left/right` or `h/l` or `enter`: Expand/collapse nodes
- `tab`: Switch between namespace tree and messages pane
- `ctrl+n` / `ctrl+p`: Next/previous page of messages
- `ctrl+y`: Copy raw message body to clipboard (message list, detail, send/resend-edit body)
- `S`: Open send message modal from selected topic/queue
- `space`: Select/deselect messages
- `R`: Resend selected messages (or current row if none selected; single selection opens editable form)
- `esc`: Go back
- `ctrl+c`: Quit

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap monsieurtib/tap
brew install service-bus-tui
```

### Winget (Windows)

```powershell
winget install MonsieurTib.service-bus-tui
```

### Chocolatey (Windows)

```powershell
choco install service-bus-tui
```

### Go Install

```bash
go install github.com/MonsieurTib/service-bus-tui@latest
```

### Build from source

```bash
git clone https://github.com/MonsieurTib/service-bus-tui.git
cd service-bus-tui
go build -o service-bus-tui .
```

## Usage

```bash
service-bus-tui
```

Select an authentication method, choose a namespace, and browse your Service Bus resources.

If you authenticate with a connection string, the app prompts you to optionally save it for later reuse.

## Emulator Support

Supports the [Azure Service Bus emulator](https://learn.microsoft.com/en-us/azure/service-bus-messaging/overview-emulator) running in Docker. Select **Emulator (localhost)** from the auth menu for default port.

**Limitation**: Total message count is unavailable (SDK bug) — page indicator shows "?" but pagination works normally.

## Saved Connection Strings

Saved connection strings are stored locally as plain JSON at:

`~/.config/service-bus-tui/connections.json`

The app writes this file with restrictive permissions (directory `0700`, file `0600`).

## Requirements

- Go 1.24+
- Azure subscription with Service Bus namespaces (or the local emulator)
- For Azure CLI auth: `az login` must be completed beforehand
- For Emulator: Docker with the [Service Bus emulator](https://github.com/Azure/azure-service-bus-emulator-installer) running
