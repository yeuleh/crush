# Custom-Gemini Provider URL Configuration

## Overview

The `custom-gemini` provider supports two URL modes to provide maximum flexibility for different deployment scenarios:

1. **Standard Mode**: Automatically constructs API endpoints by combining base URL with method paths
2. **Complete URL Mode**: Uses a complete endpoint URL directly

## URL Modes

### Standard Mode

When `base_url` does NOT end with `#`, the provider operates in standard mode.

**Configuration Example:**
```json
{
  "providers": {
    "my-custom-gemini": {
      "type": "custom-gemini",
      "base_url": "https://generativelanguage.googleapis.com/v1beta",
      "api_key": "$GEMINI_API_KEY",
      "models": [...]
    }
  }
}
```

**Behavior:**
- Base URL + method path → Complete request URL
- Example: `https://generativelanguage.googleapis.com/v1beta` + `models/gemini-pro:generateContent` → `https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent`
- Query parameters in base URL are preserved unless overridden by method path

### Complete URL Mode

When `base_url` ends with a single `#`, the provider operates in complete URL mode.

**Configuration Example:**
```json
{
  "providers": {
    "my-proxy-gemini": {
      "type": "custom-gemini", 
      "base_url": "https://my-proxy.com/api/gemini/chat?key=abc123#",
      "api_key": "$PROXY_API_KEY",
      "models": [...]
    }
  }
}
```

**Behavior:**
- The `#` is removed and the URL is used directly as the endpoint
- Method paths are ignored in this mode
- Useful for custom proxy endpoints or pre-configured URLs

## Environment Variables

The provider checks environment variables in the following priority order:

1. `CRUSH_GEMINI_BASE_URL` (highest priority)
2. `GEMINI_BASE_URL`
3. `GOOGLE_GEMINI_BASE_URL`
4. Default: `https://generativelanguage.googleapis.com` (lowest priority)

Environment variables can also use the `#` suffix for complete URL mode.

## Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| `invalid base URL: empty base URL` | Base URL is empty | Provide a valid base URL |
| `invalid URL scheme: unsupported scheme 'ftp'` | Unsupported URL scheme | Use `http` or `https` |
| `ambiguous full URL marker: multiple trailing '#' characters` | Multiple `#` at end | Use only single `#` for complete URL mode |
| `ambiguous full URL marker: '#' character is only allowed at the end` | `#` in middle of URL | Move `#` to the end or remove it |
| `invalid method path: empty method path` | Method path is empty in standard mode | Provide a valid method path |

## Migration Guide

### From existing `gemini` provider

No changes needed! Your existing configuration will continue to work unchanged.

### To enable complete URL mode

Simply add `#` to the end of your `base_url`:

**Before:**
```json
"base_url": "https://my-endpoint.com/api/gemini"
```

**After:**
```json
"base_url": "https://my-endpoint.com/api/gemini#"
```

## Examples

### Standard Gemini API
```json
{
  "type": "custom-gemini",
  "base_url": "https://generativelanguage.googleapis.com/v1beta",
  "api_key": "$GEMINI_API_KEY"
}
```

### Custom Proxy with Authentication
```json
{
  "type": "custom-gemini",
  "base_url": "https://my-proxy.example.com/gemini/v1/chat?auth=token123#",
  "api_key": "$PROXY_API_KEY"
}
```

### Using Environment Variables
Set `CRUSH_GEMINI_BASE_URL=https://my-custom-endpoint.com` and use:
```json
{
  "type": "custom-gemini",
  "api_key": "$GEMINI_API_KEY"
}
```

The provider will automatically use the environment variable as the base URL.
