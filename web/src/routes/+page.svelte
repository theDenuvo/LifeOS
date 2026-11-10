<script lang="ts">
	import { onMount } from 'svelte';
	import { Check, X, Loader2 } from 'lucide-svelte';

	let healthStatus = 'checking';

	onMount(async () => {
		try {
			const res = await fetch('/healthz');
			if (res.ok) {
				healthStatus = 'ok';
			} else {
				healthStatus = 'error';
			}
		} catch {
			healthStatus = 'error';
		}
	});
</script>

<main class="container">
	<div class="card">
		<div class="logo">
			<svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="text-accent">
				<path d="M12 2L2 7l10 5 10-5-10-5z" />
				<path d="M2 17l10 5 10-5" />
				<path d="M2 12l10 5 10-5" />
			</svg>
		</div>
		<h1>LifeOS</h1>
		<p class="subtitle">Personal Operating System with Intelligent Assistant</p>

		<div class="status">
			{#if healthStatus === 'checking'}
				<Loader2 class="spin" />
				<span>Checking backend...</span>
			{:else if healthStatus === 'ok'}
				<Check class="text-success" />
				<span>Backend connected</span>
			{:else}
				<X class="text-error" />
				<span>Backend unavailable</span>
			{/if}
		</div>

		<p class="version">Foundation Phase — Task Engine coming next</p>
	</div>
</main>

<style>
	.container {
		min-height: 100vh;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 2rem;
	}

	.card {
		background: var(--bg-card, #12121a);
		border: 1px solid var(--border, #2a2a3a);
		border-radius: 16px;
		padding: 3rem 2rem;
		max-width: 400px;
		width: 100%;
		text-align: center;
		box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
	}

	.logo {
		margin-bottom: 1rem;
	}

	h1 {
		font-size: 2.5rem;
		font-weight: 700;
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		-webkit-background-clip: text;
		-webkit-text-fill-color: transparent;
		background-clip: text;
		margin: 0 0 0.5rem;
	}

	.subtitle {
		color: var(--fg-muted, #71717a);
		margin: 0 0 2rem;
		font-size: 1rem;
	}

	.status {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 0.5rem;
		padding: 1rem;
		background: var(--bg-primary, #0a0a0f);
		border-radius: 8px;
		margin-bottom: 1.5rem;
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.875rem;
	}

	.status svg {
		width: 20px;
		height: 20px;
	}

	.status .spin {
		animation: spin 1s linear infinite;
		color: var(--accent, #8b5cf6);
	}

	@keyframes spin {
		from { transform: rotate(0deg); }
		to { transform: rotate(360deg); }
	}

	.text-success { color: #22c55e; }
	.text-error { color: #ef4444; }
	.text-accent { color: #8b5cf6; }

	.version {
		color: var(--fg-muted, #71717a);
		font-size: 0.75rem;
		margin: 0;
	}
</style>
