import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';

export default defineConfig({
	plugins: [
		sveltekit(),
		SvelteKitPWA({
			registerType: 'autoUpdate',
			includeAssets: ['favicon.ico', 'robots.txt'],
			manifest: {
				name: 'LifeOS',
				short_name: 'LifeOS',
				description: 'Personal Operating System with Intelligent Assistant',
				theme_color: '#8b5cf6',
				background_color: '#0a0a0f',
				display: 'standalone',
				scope: '/',
				start_url: '/',
				icons: [
					{
						src: '/icon-192.svg',
						sizes: '192x192',
						type: 'image/svg+xml',
						purpose: 'any maskable'
					},
					{
						src: '/icon-512.svg',
						sizes: '512x512',
						type: 'image/svg+xml',
						purpose: 'any maskable'
					}
				]
			},
			workbox: {
				globPatterns: ['**/*.{js,css,html,ico,svg,woff2}'],
				runtimeCaching: [
					{
						urlPattern: /^https:\/\/api\./,
						handler: 'NetworkFirst',
						options: {
							cacheName: 'api-cache',
							expiration: {
								maxEntries: 100,
								maxAgeSeconds: 60 * 60 * 24
							}
						}
					}
				]
			}
		})
	],
	server: {
		port: 5173,
		proxy: {
			'/api': {
				target: 'http://127.0.0.1:8787',
				changeOrigin: true
			},
			'/healthz': {
				target: 'http://127.0.0.1:8787',
				changeOrigin: true
			}
		}
	}
});
