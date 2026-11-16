<script lang="ts">
	import { onMount } from 'svelte';
	import {
		approveAutomation,
		changeAutomationStatus,
		deleteAutomation,
		listAutomations,
		runAutomation,
		type Automation,
		type AutomationStatus
	} from '$lib/api';
	import { errorMessage, formatDate, formatTime } from '$lib/format';
	import { liveRevision } from '$lib/live';
	import { Workflow, Check, Trash2, X, Play, PauseCircle, Archive, ChevronDown, CircleAlert, Zap } from 'lucide-svelte';

	type Tab = 'proposed' | 'active' | 'paused' | 'archived';

	const tabLabels: Record<Tab, string> = {
		proposed: 'На утверждении',
		active: 'Включены',
		paused: 'На паузе',
		archived: 'В архиве'
	};

	const statusLabels: Record<AutomationStatus, string> = {
		proposed: 'предложено',
		active: 'включена',
		paused: 'на паузе',
		archived: 'в архиве'
	};

	const runStatusLabels: Record<string, string> = {
		running: 'выполняется',
		succeeded: 'успешно',
		failed: 'с ошибкой',
		skipped: 'пропущено'
	};

	const stepStatusLabels: Record<string, string> = {
		succeeded: 'выполнен',
		failed: 'ошибка',
		skipped: 'пропущен'
	};

	const triggerKindLabels: Record<string, string> = {
		schedule: 'по времени',
		event: 'по событию',
		condition: 'по условию',
		manual: 'вручную'
	};

	const weekdays = ['вс', 'пн', 'вт', 'ср', 'чт', 'пт', 'сб'];

	let tab: Tab = 'active';
	let automations: Automation[] = [];
	let loading = true;
	let error = '';
	let notice = '';
	let busyId: number | null = null;

	let expandedId: number | null = null;
	let detail: Automation | null = null;
	let detailLoading = false;
	let expandedRunId: number | null = null;

	async function load() {
		loading = true;
		try {
			const result = await listAutomations({ status: tab as AutomationStatus, limit: 50 });
			automations = result.data;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	function selectTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		expandedId = null;
		detail = null;
		notice = '';
		load();
	}

	async function toggle(id: number) {
		if (expandedId === id) {
			expandedId = null;
			detail = null;
			return;
		}
		expandedId = id;
		detailLoading = true;
		detail = null;
		try {
			const api = await import('$lib/api');
			detail = await api.getAutomation(id);
			error = '';
		} catch (err) {
			error = errorMessage(err);
			expandedId = null;
		} finally {
			detailLoading = false;
		}
	}

	async function approve(item: Automation) {
		busyId = item.id;
		try {
			const updated = await approveAutomation(item.id);
			notice = 'Автоматизация утверждена и включена.';
			if (detail && detail.id === item.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function setStatus(item: Automation, status: 'active' | 'paused' | 'archived', message: string) {
		busyId = item.id;
		try {
			const updated = await changeAutomationStatus(item.id, status);
			notice = message;
			if (detail && detail.id === item.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function runNow(item: Automation) {
		if (!confirm(`Выполнить «${item.title}» прямо сейчас? Шаги сценария сработают сразу.`)) return;
		busyId = item.id;
		try {
			const updated = await runAutomation(item.id);
			const failed = updated.runs[0]?.status === 'failed';
			notice = failed ? 'Запуск завершился ошибкой — подробности в журнале.' : 'Запуск выполнен.';
			if (detail && detail.id === item.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function remove(item: Automation) {
		if (!confirm(`Удалить автоматизацию «${item.title}» вместе с журналом запусков?`)) return;
		busyId = item.id;
		try {
			await deleteAutomation(item.id);
			if (expandedId === item.id) {
				expandedId = null;
				detail = null;
			}
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	function describeTrigger(item: Automation): string {
		const trigger = item.trigger;
		switch (trigger.kind) {
			case 'schedule': {
				const schedule = trigger.schedule;
				if (!schedule) return 'по времени';
				let cadence = 'ежедневно';
				if (schedule.frequency === 'weekly') {
					const days = (schedule.weekdays ?? []).map((day) => weekdays[day] ?? '?').join(', ');
					cadence = `еженедельно по ${days}`;
				} else if (schedule.frequency === 'monthly') {
					cadence =
						schedule.day_of_month === 0 || !schedule.day_of_month
							? 'ежемесячно в последний день месяца'
							: `ежемесячно ${schedule.day_of_month} числа`;
				}
				return `${cadence} в ${schedule.time}`;
			}
			case 'event':
				return trigger.event ? `по событию ${trigger.event.name}` : 'по событию';
			case 'condition':
				return trigger.condition ? `пока наблюдение «${trigger.condition.insight}» активно` : 'по условию';
			default:
				return trigger.kind;
		}
	}

	function latestRun(item: Automation) {
		return item.runs[0] ?? null;
	}

	onMount(() => {
		load();
		// Автоматизации включаются и выполняются сами: событие потока обновляет
		// и список, и открытую карточку, не оставляя устаревших статусов.
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(refresh, 250);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});

	/** refresh перечитывает список и раскрытую карточку. */
	async function refresh() {
		await load();
		if (expandedId === null) return;
		try {
			const api = await import('$lib/api');
			detail = await api.getAutomation(expandedId!);
		} catch {
			expandedId = null;
			detail = null;
		}
	}
</script>

<svelte:head>
	<title>Автоматизации — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Автоматизации</h1>
		<p class="subtitle">
			Повторяющиеся процессы по согласованному сценарию: триггер, шаги и журнал запусков.
			Опишите потребность ассистенту — он спроектирует сценарий и покажет его здесь на утверждение.
		</p>
	</div>
</header>

<div class="tabs" role="tablist" aria-label="Статусы автоматизаций">
	{#each Object.entries(tabLabels) as [key, label] (key)}
		<button
			class="tab"
			class:active={tab === key}
			role="tab"
			aria-selected={tab === key}
			on:click={() => selectTab(key as Tab)}
		>
			{label}
		</button>
	{/each}
</div>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть сообщение об ошибке">×</button>
	</div>
{/if}

{#if notice}
	<div class="notice" role="status">
		<span>{notice}</span>
		<button on:click={() => (notice = '')} aria-label="Закрыть уведомление">×</button>
	</div>
{/if}

{#if loading}
	<p class="empty" role="status">Загрузка…</p>
{:else if automations.length === 0}
	<div class="empty">
		<Workflow size={28} strokeWidth={1.5} aria-hidden="true" />
		<p>
			{tab === 'proposed'
				? 'Новых сценариев нет. Опишите ассистенту повторяющийся процесс — он предложит автоматизацию.'
				: tab === 'active'
					? 'Сейчас ни одна автоматизация не включена.'
					: tab === 'paused'
						? 'Всё работает без пауз.'
						: 'Архив пуст.'}
		</p>
	</div>
{:else}
	<ul class="automation-list">
		{#each automations as item (item.id)}
			<li class="automation-card" class:expanded={expandedId === item.id}>
				<button class="card-head" on:click={() => toggle(item.id)} aria-expanded={expandedId === item.id}>
					<div class="title-row">
						<span class="title">{item.title}</span>
						<span class="trigger">{describeTrigger(item)}</span>
					</div>
					<p class="meta">
						<span class={`status-pill ${item.status}`}>{statusLabels[item.status]}</span>
						{#if item.next_run_at && item.status === 'active'}
							<span class="meta-item">следующий запуск {formatDate(item.next_run_at)} {formatTime(item.next_run_at)}</span>
						{/if}
						{#if item.last_run_at}
							<span class="meta-item">последний запуск {formatDate(item.last_run_at)}</span>
						{/if}
					</p>
					{#if latestRun(item)}
						<p class={`run-line ${latestRun(item)!.status}`}>
							<Zap size={13} aria-hidden="true" />
							{runStatusLabels[latestRun(item)!.status] ?? latestRun(item)!.status}:
							{latestRun(item)!.summary}
						</p>
					{/if}
				</button>

				{#if expandedId === item.id}
					<div class="card-detail">
						{#if detailLoading}
							<p class="empty" role="status">Загрузка…</p>
						{:else if detail}
							{#if detail.description}
								<p class="description">{detail.description}</p>
							{/if}

							<div class="facts">
								{#if detail.expected_result}
									<div class="fact">
										<span class="fact-label">Ожидаемый результат</span>
										<span class="fact-value">{detail.expected_result}</span>
									</div>
								{/if}
								{#if detail.consequences}
									<div class="fact">
										<span class="fact-label">Последствия</span>
										<span class="fact-value">{detail.consequences}</span>
									</div>
								{/if}
							</div>

							<div class="actions">
								{#if detail.status === 'proposed'}
									<button class="primary small" disabled={busyId === detail.id} on:click={() => approve(detail!)}>
										<Check size={13} aria-hidden="true" /> Утвердить и включить
									</button>
								{/if}
								{#if detail.status === 'active'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => runNow(detail!)}>
										<Play size={13} aria-hidden="true" /> Запустить сейчас
									</button>
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'paused', 'Автоматизация приостановлена.')}>
										<PauseCircle size={13} aria-hidden="true" /> На паузу
									</button>
								{/if}
								{#if detail.status === 'paused'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'active', 'Автоматизация возобновлена.')}>
										<Play size={13} aria-hidden="true" /> Возобновить
									</button>
								{/if}
								{#if detail.status === 'active' || detail.status === 'paused' || detail.status === 'proposed'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'archived', 'Автоматизация отправлена в архив.')}>
										<Archive size={13} aria-hidden="true" /> В архив
									</button>
								{/if}
								<button class="ghost small danger" disabled={busyId === detail.id} on:click={() => remove(detail!)}>
									<Trash2 size={13} aria-hidden="true" /> Удалить
								</button>
							</div>
							{#if detail.status === 'active'}
								<p class="hint">Конфигурацию включённой автоматизации меняет ассистент после паузы: попросите его и подтвердите изменение.</p>
							{/if}

							<section aria-label="Сценарий">
								<h2>Сценарий</h2>
								<ol class="steps">
									{#each detail.steps as step (step.position)}
										<li class="step">
											<div class="step-main">
												<span class="step-title">{step.description || step.name}</span>
												<span class="step-tool">{step.name}</span>
											</div>
											{#if step.arguments && Object.keys(step.arguments).length > 0}
												<details class="step-args">
													<summary>Аргументы</summary>
													<pre>{JSON.stringify(step.arguments, null, 2)}</pre>
												</details>
											{/if}
										</li>
									{/each}
								</ol>
							</section>

							<section aria-label="Журнал запусков">
								<h2>Журнал запусков</h2>
								{#if detail.runs.length === 0}
									<p class="hint">Запусков ещё не было: автоматизация сработает, когда наступит её момент.</p>
								{:else}
									<ul class="runs">
										{#each detail.runs as run (run.id)}
											<li class="run">
												<button class="run-head" on:click={() => (expandedRunId = expandedRunId === run.id ? null : run.id)} aria-expanded={expandedRunId === run.id}>
													<span class={`run-status ${run.status}`}>
														{#if run.status === 'failed'}<CircleAlert size={13} aria-hidden="true" />{:else}<Zap size={13} aria-hidden="true" />{/if}
														{runStatusLabels[run.status] ?? run.status}
													</span>
													<span class="run-trigger">{triggerKindLabels[run.trigger] ?? run.trigger}</span>
													<span class="run-when">{formatDate(run.started_at)} {formatTime(run.started_at)}</span>
													<span class="run-summary">{run.summary}</span>
													<ChevronDown size={14} class="run-chevron" aria-hidden="true" />
												</button>
												{#if expandedRunId === run.id}
													<div class="run-detail">
														{#if run.error}
															<p class="run-error">{run.error}</p>
														{/if}
														<ul class="run-steps">
															{#each run.steps as step, index (index)}
																<li class="run-step">
																	<span class={`step-status ${step.status}`}>{stepStatusLabels[step.status] ?? step.status}</span>
																	<span class="run-step-name">{step.name}</span>
																	{#if step.duration_ms !== undefined}
																		<span class="step-duration">{step.duration_ms} мс</span>
																	{/if}
																	{#if step.error}
																		<p class="step-error">{step.error}</p>
																	{/if}
																	{#if step.output}
																		<details class="step-args">
																			<summary>Результат</summary>
																			<pre>{step.output}</pre>
																		</details>
																	{/if}
																</li>
															{/each}
														</ul>
													</div>
												{/if}
											</li>
										{/each}
									</ul>
								{/if}
							</section>
						{/if}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}

	h1 {
		margin: 0;
		font-size: var(--text-2xl);
		letter-spacing: -0.02em;
	}

	h2 {
		margin: var(--space-3) 0 var(--space-2);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--fg-faint);
		font-weight: 600;
	}

	.subtitle {
		margin: var(--space-1) 0 0;
		color: var(--fg-muted);
		font-size: var(--text-md);
		max-width: 70ch;
	}

	.primary.small,
	.ghost.small {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 0.35rem 0.6rem;
		font-size: var(--text-sm);
	}

	.primary {
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: var(--radius-md);
		padding: 0.6rem 0.95rem;
		font-weight: 600;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.45rem 0.8rem;
		font-size: var(--text-base);
	}

	.ghost:hover {
		background: var(--bg-raised);
	}

	.ghost.danger {
		color: var(--danger-text);
		border-color: var(--danger-border);
	}

	.primary:disabled,
	.ghost:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.tabs {
		display: flex;
		gap: var(--space-1);
		margin-bottom: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0.55rem 0.7rem;
		border: none;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--fg-muted);
		font-size: var(--text-base);
	}

	.tab:hover {
		color: var(--fg-primary);
	}

	.tab.active {
		color: var(--fg-primary);
		border-bottom-color: var(--accent);
	}

	.banner,
	.notice {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		padding: 0.6rem 0.8rem;
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		margin-bottom: var(--space-4);
	}

	.banner {
		background: var(--danger-surface);
		border: 1px solid var(--danger-border);
		color: var(--danger-text);
	}

	.notice {
		background: var(--success-surface);
		border: 1px solid rgba(52, 211, 153, 0.3);
		color: var(--text);
	}

	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-muted);
		padding: var(--space-6) 0;
		text-align: center;
	}

	.automation-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.automation-card {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		overflow: hidden;
	}

	.automation-card.expanded {
		border-color: var(--accent-border);
	}

	.card-head {
		display: block;
		width: 100%;
		text-align: left;
		background: transparent;
		border: none;
		color: inherit;
		padding: var(--space-3) var(--space-4);
		cursor: pointer;
	}

	.card-head:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: -2px;
	}

	.title-row {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-3);
		flex-wrap: wrap;
	}

	.title {
		font-weight: 600;
		font-size: var(--text-lg);
		color: var(--fg-primary);
	}

	.trigger {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--accent-bright);
	}

	.meta {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: var(--space-3);
		margin: var(--space-1) 0 0;
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.status-pill {
		padding: 0.1rem 0.5rem;
		border-radius: 999px;
		font-size: var(--text-2xs);
		border: 1px solid var(--border);
		color: var(--fg-muted);
	}

	.status-pill.active {
		color: var(--success);
		border-color: rgba(52, 211, 153, 0.4);
	}

	.status-pill.proposed {
		color: var(--accent-bright);
		border-color: var(--accent-border);
	}

	.status-pill.paused {
		color: var(--warning);
		border-color: var(--warning-border);
	}

	.status-pill.archived {
		color: var(--fg-faint);
	}

	.run-line {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.run-line.succeeded {
		color: var(--success);
	}

	.run-line.failed {
		color: var(--warning);
	}

	.run-line.running {
		color: var(--accent-bright);
	}

	.card-detail {
		border-top: 1px solid var(--border-soft);
		padding: var(--space-3) var(--space-4) var(--space-4);
	}

	.description {
		color: var(--fg-body);
		font-size: var(--text-base);
		margin: 0 0 var(--space-3);
	}

	.facts {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
	}

	.fact {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.fact-label {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--fg-faint);
	}

	.fact-value {
		font-size: var(--text-base);
		color: var(--fg-body);
	}

	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
	}

	.hint {
		color: var(--fg-faint);
		font-size: var(--text-sm);
		margin: 0 0 var(--space-2);
	}

	.steps {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		counter-reset: step;
	}

	.step {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		padding: var(--space-2) 0;
		border-bottom: 1px solid var(--border-soft);
	}

	.step::before {
		counter-increment: step;
		content: counter(step);
		flex-shrink: 0;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.step:last-child {
		border-bottom: none;
	}

	.step-main {
		flex: 1;
		min-width: 0;
	}

	.step-title {
		display: block;
		color: var(--fg-primary);
	}

	.step-tool {
		display: block;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-faint);
	}

	.step-args {
		margin-top: var(--space-1);
	}

	.step-args summary {
		cursor: pointer;
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.step-args pre {
		margin: var(--space-1) 0 0;
		padding: var(--space-2);
		background: var(--bg-primary);
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-md);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-muted);
		overflow-x: auto;
	}

	.runs {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}

	.run {
		border-bottom: 1px solid var(--border-soft);
	}

	.run:last-child {
		border-bottom: none;
	}

	.run-head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		width: 100%;
		padding: var(--space-2) 0;
		background: transparent;
		border: none;
		color: inherit;
		text-align: left;
		cursor: pointer;
	}

	.run-head:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: -2px;
	}

	.run-status {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		flex-shrink: 0;
		font-size: var(--text-xs);
	}

	.run-status.succeeded {
		color: var(--success);
	}

	.run-status.failed {
		color: var(--warning);
	}

	.run-status.running {
		color: var(--accent-bright);
	}

	.run-status.skipped {
		color: var(--fg-faint);
	}

	.run-trigger {
		flex-shrink: 0;
		font-size: var(--text-xs);
		color: var(--fg-faint);
	}

	.run-when {
		flex-shrink: 0;
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.run-summary {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-sm);
		color: var(--fg-body);
	}

	.run-chevron {
		flex-shrink: 0;
		color: var(--fg-faint);
		transform: rotate(0deg);
	}

	.run-detail {
		padding: 0 0 var(--space-2) var(--space-4);
	}

	.run-error {
		margin: 0 0 var(--space-2);
		padding: var(--space-2) var(--space-3);
		background: var(--danger-surface);
		border: 1px solid var(--danger-border);
		border-radius: var(--radius-md);
		color: var(--danger-text);
		font-size: var(--text-sm);
	}

	.run-steps {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	.run-step {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: var(--space-2);
		padding: var(--space-1) 0;
	}

	.step-status {
		flex-shrink: 0;
		font-size: var(--text-xs);
	}

	.step-status.succeeded {
		color: var(--success);
	}

	.step-status.failed {
		color: var(--danger-text);
	}

	.step-status.skipped {
		color: var(--fg-faint);
	}

	.run-step-name {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-body);
	}

	.step-duration {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-faint);
	}

	.step-error {
		width: 100%;
		margin: 0;
		color: var(--danger-text);
		font-size: var(--text-sm);
	}

	@media (max-width: 720px) {
		.run-head {
			flex-wrap: wrap;
		}

		.run-summary {
			white-space: normal;
		}
	}
</style>
