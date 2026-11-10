<script lang="ts">
	import '../app.css';
</script>

<div class="app">
	<slot />
</div>

<style>
	.app {
		min-height: 100vh;
		background: var(--bg-primary, #0a0a0f);
		color: var(--fg-primary, #e4e4e7);
		font-family: 'Inter', system-ui, sans-serif;
	}
</style>
