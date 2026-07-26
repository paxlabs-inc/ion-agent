import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv, type Plugin } from 'vite'

export default defineConfig(({ command, mode }) => {
  const environment = loadEnv(mode, '.', '')
  const actorID = environment.ION_WEB_ACTOR_ID
  return {
  plugins: [react(), devActorBootstrap(command === 'serve' ? actorID : undefined), trimGeneratedWhitespace()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    proxy: {
      '/v1': { target: 'http://127.0.0.1:4176', changeOrigin: true, ws: true },
    },
  },
  build: {
    target: 'es2022',
    sourcemap: false,
    manifest: true,
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom', 'react-router-dom'],
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    css: true,
    testTimeout: 15_000,
  },
  }
})

function devActorBootstrap(actorID: string | undefined): Plugin {
  return {
    name: 'ion-dev-actor-bootstrap',
    transformIndexHtml() {
      if (actorID === undefined || actorID.trim() === '') return []
      return [{ tag: 'meta', attrs: { name: 'ion-actor-id', content: actorID }, injectTo: 'head' }]
    },
  }
}

function trimGeneratedWhitespace(): Plugin {
  return {
    name: 'trim-generated-trailing-whitespace',
    enforce: 'post',
    generateBundle(_options, bundle) {
      for (const output of Object.values(bundle)) {
        if (output.type === 'chunk') {
          output.code = output.code.replaceAll(/[ \t]+$/gm, '')
        }
      }
    },
  }
}
