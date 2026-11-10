import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { sveltekit } from '@sveltejs/kit/vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),

	kit: {
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: 'index.html',
			precompress: false,
			strict: true
		}),
		paths: {
			base: ''
		},
		prerender: {
			entries: ['*'],
			handleHttpError: 'warn'
		},
		alias: {
			$lib: 'src/lib'
		}
	},

	plugins: [
		svelte(),
		sveltekit()
	]
};

export default defineConfig(config);