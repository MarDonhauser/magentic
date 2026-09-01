import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

const root = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
  build: {
    // The native WKWebView loads notch.html as one in-memory document. Keeping
    // each entry self-contained avoids a relative module-preload import from
    // an about:blank base URL after the assets are inlined by Go.
    modulePreload: false,
    rollupOptions: {
      input: {
        main: resolve(root, 'index.html'),
        notch: resolve(root, 'notch.html'),
      },
    },
  },
});
