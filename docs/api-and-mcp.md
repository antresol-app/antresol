# API keys and AI assistants (MCP)

An API key lets an AI assistant — Claude, ChatGPT or any other
[MCP](https://modelcontextprotocol.io) client — or your own script use the
server on your behalf: find a film, say what is on TV, mark an episode
watched, start a download.

## Create a key

**Settings → My Settings → Apps & Devices → API Keys & AI → Create Key.** Give it a
name and choose what it may do:

- **Read Only** — search the library, see details, the episode calendar and
  what is on TV. Changes nothing.
- **Read & Act** — also mark titles watched and start downloads (when the
  server has a download addon).
- **Administration** (admins only) — also scan the library, restart addons
  and list the profiles.

The key is shown once; copy it then. The page lists your keys with when
each was last used, revokes a key at once, and shows the last changes made
with keys. A key never has more rights than its profile, and no key can
create keys, change passwords or sign devices in. Kid profiles have no
keys. A profile without a password can use its keys only from the home
network.

## Connect an assistant

The MCP server is at `/mcp` on your server's address (the page uses the
[public address](public-address.html), when it is set). The page shows these
lines with your address and key filled in.

Claude Code:

```sh
claude mcp add --transport http antresol https://media.example.org/mcp \
  --header "Authorization: Bearer YOUR_KEY"
```

Claude Desktop (`claude_desktop_config.json`, needs Node.js):

```json
{ "mcpServers": { "antresol": { "command": "npx",
  "args": ["-y", "mcp-remote", "https://media.example.org/mcp",
           "--header", "Authorization: Bearer YOUR_KEY"] } } }
```

Any other client: the Streamable HTTP transport at
`https://media.example.org/mcp` with the header
`Authorization: Bearer YOUR_KEY`.

Tools: `search_library`, `get_item`, `continue_watching`, `recently_added`,
`tv_now`, `upcoming_episodes`, `server_status`, `discover_titles`;
with Read & Act also `set_watched`, `download_title`; with Administration
also `scan_library`, `restart_addon`, `list_profiles`. No tool deletes
anything.

## The API

The same key works with the server's HTTP API: send
`Authorization: Bearer YOUR_KEY` to the `/api/…` routes the web app uses.
A Read Only key gets `GET` requests only. A key may make about 120 requests
a minute (`429` with `Retry-After` beyond that).
