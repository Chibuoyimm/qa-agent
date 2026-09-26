# QA Agent web workspace

This is the local pilot UI for configuring projects, reviewing scenario definitions, launching checks, and inspecting results. It connects to the Go API through Vite's `/api` proxy at `http://127.0.0.1:8080` by default. Set `QA_API_BASE_URL` for another local API address.

```sh
npm ci
npm run dev
```

Enter the server's `QA_API_TOKEN` in the workspace. The token is held only in React memory and disappears on reload; it is not written to browser storage. The API must be running and the project's target origin must be in `QA_ALLOWED_ORIGINS`.

Run `npm run check` and `npm run build` for type and production checks.
