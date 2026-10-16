import { defineConfig } from 'vitest/config';

export default defineConfig({
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
