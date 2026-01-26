# asb-tui

A terminal-based explorer for Azure Service Bus.

> **Work in Progress**: This project is currently under active development.

## Features

### Authentication
- Azure CLI authentication (uses existing `az login` session)
- Interactive browser authentication
  
Planned authentication methods:
- Connection string
- Service principal (client ID / client secret)

### Namespace Discovery
- Automatically lists all Service Bus namespaces across your Azure subscriptions
- Displays namespace name, subscription ID, and resource group

### Resource Browsing
- Tree-based navigation of namespaces
- List topics and queues
- Expand topics to view subscriptions
- View active messages and dead-letter queue (DLQ) messages per subscription

### Message Viewing
- Peek messages from subscriptions (active and DLQ)
- Tabular display with sequence number, message ID, subject, enqueued time, and body preview
- JSON body formatting in preview

### Navigation
- Keyboard-driven interface
- `up/down` or `j/k`: Navigate items
- `left/right` or `h/l` or `enter`: Expand/collapse nodes
- `tab`: Switch between namespace tree and messages pane
- `esc`: Go back
- `ctrl+c`: Quit

## Installation

```bash
go install github.com/popandcode/asb-tui@latest
```

Or build from source:

```bash
git clone https://github.com/popandcode/asb-tui.git
cd asb-tui
go build -o asb-tui .
```

## Usage

```bash
asb-tui
```

Select an authentication method, choose a namespace, and browse your Service Bus resources.

## Requirements

- Go 1.21+
- Azure subscription with Service Bus namespaces
- For Azure CLI auth: `az login` must be completed beforehand
