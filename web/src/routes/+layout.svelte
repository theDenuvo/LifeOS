<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { startLive } from '$lib/live';

	const nav = [
		{ href: '/', label: 'Сегодня', icon: 'M12 7v5l3 3M21 12a9 9 0 11-18 0 9 9 0 0118 0z' },
		{ href: '/inbox', label: 'Входящие', icon: 'M4 6h16l-2 14H6L4 6zm4 4h8m-8 4h5' },
		{ href: '/tasks', label: 'Задачи', icon: 'M4 5h16v14H4zM4 10h16M9 5v14' },
		{ href: '/assistant', label: 'Ассистент', icon: 'M12 3a9 9 0 100 18 9 9 0 000-18zm0 5v4m0 3v.01' }
	];

	$: current = $page.url.pathname;

	let question = '';

	function ask() {
		const text = question.trim();
		if (!text) return;
		question = '';
		goto(`/assistant?q=${encodeURIComponent(text)}`);
	}

	onMount(startLive);
</script>

<div class="shell">
	<aside class="sidebar">
		<a class="brand" href="/">
			<span class="brand-mark">L</span>
			<span class="brand-text">LifeOS</span>
		</a>

		<nav aria-label="Основная навигация">
			{#each nav as item}
				<a
					class="nav-item"
					class:active={current === item.href}
					href={item.href}
					aria-current={current === item.href ? 'page' : undefined}
				>
					<svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
						<path d={item.icon} stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
					</svg>
					<span>{item.label}</span>
				</a>
			{/each}
		</nav>

		<form class="ask" on:submit|preventDefault={ask}>
			<input
				bind:value={question}
				placeholder="Спросить LifeOS…"
				aria-label="Спросить LifeOS"
				autocomplete="off"
			/>
			<button type="submit" aria-label="Отправить вопрос">→</button>
		</form>

		<div class="sidebar-footer">
			<span class="dot" aria-hidden="true"></span>
			<span>Локальные данные · SQLite</span>
		</div>
	</aside>

	<main class="content">
		<slot />
	</main>

	<form class="mobile-ask" on:submit|preventDefault={ask}>
		<input
			bind:value={question}
			placeholder="Спросить LifeOS…"
			aria-label="Спросить LifeOS"
			autocomplete="off"
		/>
		<button type="submit" aria-label="Отправить вопрос">→</button>
	</form>

	<nav class="mobile-nav" aria-label="Мобильная навигация">
		{#each nav as item}
			<a class="mobile-item" class:active={current === item.href} href={item.href}>
				<svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
					<path d={item.icon} stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
				</svg>
				<span>{item.label}</span>
			</a>
		{/each}
	</nav>
</div>

<style>
	.shell {
		display: grid;
		grid-template-columns: 232px minmax(0, 1fr);
		min-height: 100vh;
	}

	.sidebar {
		display: flex;
		flex-direction: column;
		gap: 1.5rem;
		padding: 1.5rem 1rem;
		border-right: 1px solid var(--border);
		background: rgba(18, 18, 26, 0.6);
		position: sticky;
		top: 0;
		height: 100vh;
	}

	.brand {
		display: flex;
		align-items: center;
		gap: 0.65rem;
		padding: 0.25rem 0.5rem;
		color: var(--fg-primary);
		text-decoration: none;
	}

	.brand-mark {
		display: grid;
		place-items: center;
		width: 32px;
		height: 32px;
		border-radius: 10px;
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		font-weight: 700;
		color: #0a0a0f;
	}

	.brand-text {
		font-weight: 650;
		letter-spacing: -0.01em;
	}

	nav {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}

	.nav-item {
		display: flex;
		align-items: center;
		gap: 0.7rem;
		padding: 0.6rem 0.7rem;
		border-radius: 10px;
		color: var(--fg-muted);
		text-decoration: none;
		font-size: 0.92rem;
		transition: background 120ms ease, color 120ms ease;
	}

	.nav-item svg {
		width: 20px;
		height: 20px;
	}

	.nav-item:hover {
		background: rgba(139, 92, 246, 0.08);
		color: var(--fg-primary);
	}

	.nav-item.active {
		background: rgba(139, 92, 246, 0.14);
		color: #c4b5fd;
	}

	.ask {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		margin-top: auto;
		padding: 0.25rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: rgba(255, 255, 255, 0.03);
	}

	.ask input,
	.mobile-ask input {
		flex: 1;
		min-width: 0;
		border: none;
		background: none;
		color: var(--fg-primary);
		font: inherit;
		font-size: 0.82rem;
		padding: 0.45rem 0.5rem;
		outline: none;
	}

	.ask button,
	.mobile-ask button {
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border: none;
		border-radius: 9px;
		background: rgba(139, 92, 246, 0.2);
		color: #c4b5fd;
		font-size: 1rem;
		cursor: pointer;
	}

	.ask button:hover,
	.mobile-ask button:hover {
		background: rgba(139, 92, 246, 0.32);
	}

	.sidebar-footer {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.72rem;
		color: var(--fg-muted);
		padding: 0.5rem;
	}

	.dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: #22c55e;
	}

	.content {
		padding: 2rem clamp(1rem, 4vw, 3rem) 4rem;
		max-width: 1100px;
		width: 100%;
	}

	.mobile-nav {
		display: none;
	}

	.mobile-ask {
		display: none;
	}

	@media (max-width: 760px) {
		.shell {
			grid-template-columns: 1fr;
		}

		.sidebar {
			display: none;
		}

		.content {
			padding: 1.25rem 1rem calc(8rem + env(safe-area-inset-bottom));
		}

		.mobile-ask {
			display: flex;
			align-items: center;
			gap: 0.35rem;
			position: fixed;
			left: 0.75rem;
			right: 0.75rem;
			bottom: calc(4.4rem + env(safe-area-inset-bottom));
			padding: 0.3rem;
			border: 1px solid var(--border);
			border-radius: 12px;
			background: rgba(18, 18, 26, 0.96);
			backdrop-filter: blur(12px);
			z-index: 19;
		}

		.mobile-nav {
			display: grid;
			grid-template-columns: repeat(4, 1fr);
			position: fixed;
			inset: auto 0 0 0;
			background: rgba(10, 10, 15, 0.96);
			border-top: 1px solid var(--border);
			padding: 0.4rem 0.25rem calc(0.4rem + env(safe-area-inset-bottom));
			backdrop-filter: blur(12px);
			z-index: 20;
		}

		.mobile-item {
			display: flex;
			flex-direction: column;
			align-items: center;
			gap: 0.2rem;
			padding: 0.35rem;
			font-size: 0.68rem;
			color: var(--fg-muted);
			text-decoration: none;
		}

		.mobile-item svg {
			width: 22px;
			height: 22px;
		}

		.mobile-item.active {
			color: #c4b5fd;
		}
	}
</style>
