<script lang="ts">
	import { onMount } from 'svelte';
	import { getSummary, type Summary } from '$lib/api';
	import { errorMessage } from '$lib/format';

	let summary: Summary | null = null;
	let error = '';

	onMount(async () => {
		try {
			summary = await getSummary();
		} catch (err) {
			error = errorMessage(err);
		}
	});
</script>

<svelte:head>
	<title>Ассистент — LifeOS</title>
</svelte:head>

<header class="page-header">
	<h1>Ассистент</h1>
	<p class="subtitle">Центральный интерфейс LifeOS</p>
</header>

{#if error}
	<div class="banner" role="alert">{error}</div>
{/if}

<section class="panel">
	<h2>Скоро здесь</h2>
	<p>
		Ассистент будет работать с теми же данными, что и остальные экраны: понимать запрос, выбирать инструменты
		(задачи, входящие, расписание) и выполнять действия с подтверждением.
	</p>
	<p class="muted">
		Сейчас приложение уже умеет хранить задачи и входящие, считать сводку дня и соблюдать optimistic locking.
		Следующий этап — runtime ассистента, история диалогов и подтверждаемые действия.
	</p>
</section>

{#if summary}
	<section class="context">
		<h2>Контекст, который уже доступен</h2>
		<div class="grid">
			<div class="cell">
				<span class="value">{summary.stats.active_total}</span>
				<span class="label">активных задач</span>
			</div>
			<div class="cell">
				<span class="value">{summary.stats.overdue_count}</span>
				<span class="label">просрочено</span>
			</div>
			<div class="cell">
				<span class="value">{summary.stats.inbox_count}</span>
				<span class="label">во входящих</span>
			</div>
			<div class="cell">
				<span class="value">{summary.stats.completed_today}</span>
				<span class="label">выполнено сегодня</span>
			</div>
		</div>
	</section>
{/if}

<style>
	.page-header {
		margin-bottom: 1.5rem;
	}

	h1 {
		margin: 0;
		font-size: 1.9rem;
		letter-spacing: -0.02em;
	}

	.subtitle {
		margin: 0.25rem 0 0;
		color: var(--fg-muted);
		font-size: 0.9rem;
	}

	.panel {
		padding: 1.25rem 1.4rem;
		border: 1px solid var(--border);
		border-radius: 14px;
		background: var(--bg-card);
		margin-bottom: 1.5rem;
	}

	.panel h2,
	.context h2 {
		margin: 0 0 0.6rem;
		font-size: 1rem;
	}

	.panel p {
		margin: 0 0 0.6rem;
		line-height: 1.55;
		font-size: 0.9rem;
	}

	.banner {
		padding: 0.7rem 0.9rem;
		margin-bottom: 1rem;
		border: 1px solid rgba(248, 113, 113, 0.4);
		background: rgba(248, 113, 113, 0.08);
		color: #fca5a5;
		border-radius: 10px;
		font-size: 0.85rem;
	}

	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
		gap: 0.6rem;
	}

	.cell {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		padding: 0.8rem 0.9rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: var(--bg-card);
	}

	.value {
		font-size: 1.35rem;
		font-weight: 650;
	}

	.label {
		font-size: 0.72rem;
		color: var(--fg-muted);
	}

	.muted {
		color: var(--fg-muted);
	}
</style>
