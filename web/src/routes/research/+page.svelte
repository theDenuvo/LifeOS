<script lang="ts">
	import { onMount } from 'svelte';
	import {
		checkResearchWatch,
		callResearchSource,
		createResearchSource,
		createResearchWatch,
		deleteResearchReport,
		deleteResearchSource,
		deleteResearchWatch,
		getResearchReport,
		listResearchCalls,
		listResearchReports,
		listResearchSources,
		listResearchWatches,
		runResearch,
		updateResearchSource,
		updateResearchWatch,
		type ResearchAccessMode,
		type ResearchCadence,
		type ResearchCall,
		type ResearchFinding,
		type ResearchReport,
		type ResearchSource,
		type ResearchSourceKind,
		type ResearchTrust,
		type ResearchWatch
	} from '$lib/api';
	import { errorMessage, formatDate, formatTime } from '$lib/format';
	import { renderMarkdown } from '$lib/markdown';
	import { liveRevision } from '$lib/live';
	import {
		Telescope,
		Search,
		Trash2,
		ChevronDown,
		CircleAlert,
		ExternalLink,
		RefreshCw,
		Plus,
		PauseCircle,
		Play,
		ShieldCheck,
		ShieldOff,
		Globe,
		Plug,
		Rss
	} from 'lucide-svelte';

	type Tab = 'reports' | 'watches' | 'sources';

	const tabLabels: Record<Tab, string> = {
		reports: 'Исследования',
		watches: 'Темы',
		sources: 'Источники'
	};

	const certaintyLabels: Record<string, string> = {
		fact: 'факт',
		assumption: 'предположение',
		inference: 'вывод'
	};

	const statusLabels: Record<string, string> = {
		running: 'идёт',
		completed: 'готово',
		failed: 'ошибка'
	};

	const kindLabels: Record<ResearchSourceKind, string> = {
		web_search: 'поиск',
		http_api: 'HTTP API',
		mcp: 'MCP-сервер',
		script: 'скрипт'
	};

	const modeLabels: Record<ResearchAccessMode, string> = {
		read: 'чтение',
		write: 'чтение и отправка',
		sync: 'синхронизация'
	};

	const trustLabels: Record<ResearchTrust, string> = {
		high: 'высокое доверие',
		normal: 'обычное доверие',
		low: 'низкое доверие'
	};

	const cadenceLabels: Record<ResearchCadence, string> = {
		daily: 'каждый день',
		weekly: 'каждую неделю',
		manual: 'только вручную'
	};

	const outcomeLabels: Record<string, string> = {
		ok: 'выполнено',
		refused: 'отказано',
		failed: 'ошибка',
		started: 'отправляется',
		unknown: 'результат неизвестен'
	};

	let tab: Tab = 'reports';
	let loading = true;
	let busy = false;
	let error = '';
	let notice = '';

	// Исследования
	let reports: ResearchReport[] = [];
	let expandedReportId: number | null = null;
	let reportDetail: ResearchReport | null = null;
	let reportDetailLoading = false;
	let formOpen = false;
	let question = '';
	let queryText = '';
	let saveKnowledge = true;

	// Темы
	let watches: ResearchWatch[] = [];
	let watchFormOpen = false;
	let watchTopic = '';
	let watchQuery = '';
	let watchCadence: ResearchCadence = 'daily';
	let watchChange: Record<number, string> = {};

	// Источники
	let sources: ResearchSource[] = [];
	let expandedSourceId: number | null = null;
	let sourceCalls: ResearchCall[] = [];
	let sourceFormOpen = false;
	let sourceName = '';
	let sourceKind: ResearchSourceKind = 'http_api';
	let sourceEndpoint = '';
	let sourceMode: ResearchAccessMode = 'read';
	let sourceTrust: ResearchTrust = 'normal';
	let sourceNotes = '';

	async function loadReports() {
		try {
			const result = await listResearchReports({ limit: 40 });
			reports = result.data;
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function loadWatches() {
		try {
			const result = await listResearchWatches(true);
			watches = result.data;
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function loadSources() {
		try {
			const result = await listResearchSources();
			sources = result.data;
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function loadAll() {
		loading = true;
		await Promise.all([loadReports(), loadWatches(), loadSources()]);
		loading = false;
	}

	function selectTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		notice = '';
		expandedReportId = null;
		reportDetail = null;
		expandedSourceId = null;
	}

	function splitQueries(value: string): string[] {
		return value
			.split('\n')
			.map((line) => line.trim())
			.filter((line) => line.length > 0);
	}

	async function investigate() {
		const text = question.trim();
		if (!text || busy) return;
		busy = true;
		error = '';
		notice = '';
		try {
			const report = await runResearch({
				question: text,
				queries: splitQueries(queryText),
				save_knowledge: saveKnowledge
			});
			// Исследование с ошибкой — тоже отчёт: причина видна в карточке,
			// а не растворяется в общем сообщении «не получилось».
			if (report.status === 'failed') {
				error = report.error || 'Исследование не выполнено.';
			} else {
				notice = report.knowledge_entry_id
					? 'Исследование готово, результат сохранён в базу знаний.'
					: 'Исследование готово.';
			}
			formOpen = false;
			question = '';
			queryText = '';
			await loadReports();
			expandedReportId = report.id;
			reportDetail = report;
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function toggleReport(id: number) {
		if (expandedReportId === id) {
			expandedReportId = null;
			reportDetail = null;
			return;
		}
		expandedReportId = id;
		reportDetail = null;
		reportDetailLoading = true;
		try {
			reportDetail = await getResearchReport(id);
			error = '';
		} catch (err) {
			error = errorMessage(err);
			expandedReportId = null;
		} finally {
			reportDetailLoading = false;
		}
	}

	async function removeReport(report: ResearchReport) {
		if (!confirm(`Удалить исследование «${report.question}»? Запись в базе знаний останется.`)) return;
		busy = true;
		try {
			await deleteResearchReport(report.id);
			if (expandedReportId === report.id) {
				expandedReportId = null;
				reportDetail = null;
			}
			await loadReports();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function addWatch() {
		const topic = watchTopic.trim();
		const query = watchQuery.trim() || topic;
		if (!topic || busy) return;
		busy = true;
		error = '';
		try {
			await createResearchWatch({ topic, query, cadence: watchCadence });
			notice = `Наблюдение за темой «${topic}» создано.`;
			watchFormOpen = false;
			watchTopic = '';
			watchQuery = '';
			await loadWatches();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function checkWatch(watch: ResearchWatch) {
		busy = true;
		error = '';
		try {
			const result = await checkResearchWatch(watch.id);
			watchChange = {
				...watchChange,
				[watch.id]: result.change
					? `${result.change.summary}. Новых: ${result.change.new.length}, обновлённых: ${result.change.updated.length}.`
					: result.error ?? 'Проверка выполнена.'
			};
			await loadWatches();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function setWatchEnabled(watch: ResearchWatch, enabled: boolean) {
		busy = true;
		try {
			await updateResearchWatch(watch.id, { enabled, expected_version: watch.version });
			await loadWatches();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function removeWatch(watch: ResearchWatch) {
		if (!confirm(`Прекратить наблюдение за темой «${watch.topic}»?`)) return;
		busy = true;
		try {
			await deleteResearchWatch(watch.id);
			await loadWatches();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function addSource() {
		const name = sourceName.trim();
		const endpoint = sourceEndpoint.trim();
		if (!name || !endpoint || busy) return;
		busy = true;
		error = '';
		try {
			await createResearchSource({
				name,
				kind: sourceKind,
				endpoint,
				mode: sourceKind === 'script' ? 'read' : sourceMode,
				trust: sourceTrust,
				format: sourceKind === 'web_search' ? 'searxng' : undefined,
				notes: sourceNotes.trim(),
				enabled: true
			});
			notice = `Источник «${name}» подключён.`;
			sourceFormOpen = false;
			sourceName = '';
			sourceEndpoint = '';
			sourceNotes = '';
			await loadSources();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function setSourceEnabled(source: ResearchSource, enabled: boolean) {
		busy = true;
		try {
			await updateResearchSource(source.id, { enabled, expected_version: source.version });
			await loadSources();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	// Which tools of an MCP source may run without a write is the user's
	// decision, not the server's claim about itself: the list is edited by the
	// user and never widened by anyone else.
	async function saveAllowedTools(source: ResearchSource, value: string) {
		const tools = value
			.split(',')
			.map((tool) => tool.trim())
			.filter((tool) => tool.length > 0);
		busy = true;
		try {
			await updateResearchSource(source.id, {
				allowed_tools: tools,
				expected_version: source.version
			});
			notice = tools.length
				? `Разрешённые инструменты источника «${source.name}»: ${tools.join(', ')}.`
				: `Список разрешённых инструментов источника «${source.name}» очищен: без записи ничего не выполняется.`;
			await loadSources();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function setSourceMode(source: ResearchSource, mode: ResearchAccessMode) {
		busy = true;
		try {
			await updateResearchSource(source.id, { mode, expected_version: source.version });
			notice = `Режим источника «${source.name}»: ${modeLabels[mode]}.`;
			await loadSources();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function removeSource(source: ResearchSource) {
		if (!confirm(`Удалить источник «${source.name}»? Журнал обращений сохранится.`)) return;
		busy = true;
		try {
			await deleteResearchSource(source.id);
			if (expandedSourceId === source.id) {
				expandedSourceId = null;
				sourceCalls = [];
			}
			await loadSources();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	async function toggleSource(source: ResearchSource) {
		if (expandedSourceId === source.id) {
			expandedSourceId = null;
			sourceCalls = [];
			return;
		}
		expandedSourceId = source.id;
		sourceCalls = [];
		try {
			const result = await listResearchCalls(source.id, 10);
			sourceCalls = result.data;
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function testSource(source: ResearchSource) {
		busy = true;
		error = '';
		try {
			const result = await callResearchSource(source.id, {
				operation: source.kind === 'mcp' ? 'tools/list' : '',
				method: source.kind === 'web_search' || source.kind === 'script' ? '' : 'GET',
				query: source.kind === 'web_search' ? 'LifeOS' : ''
			});
			// Ответ целиком остаётся в журнале: здесь достаточно того, что
			// источник ответил и за сколько.
			notice = `Источник «${source.name}» ответил: ${result.status}, ${result.duration_ms} мс, ${result.body.length} символов. Ответ — в журнале обращений.`;
			const result2 = await listResearchCalls(source.id, 10);
			sourceCalls = result2.data;
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}

	onMount(() => {
		loadAll();
		// Исследования идут в фоне, наблюдения проверяются по расписанию:
		// событие потока обновляет списки и открытую карточку, чтобы статус
		// «идёт» не оставался на экране после завершения.
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

	/** refresh перечитывает списки и раскрытую карточку. */
	async function refresh() {
		await Promise.all([loadReports(), loadWatches(), loadSources()]);
		if (expandedReportId !== null) {
			try {
				reportDetail = await getResearchReport(expandedReportId);
			} catch {
				expandedReportId = null;
				reportDetail = null;
			}
		}
		if (expandedSourceId !== null) {
			try {
				sourceCalls = (await listResearchCalls(expandedSourceId, 10)).data;
			} catch {
				expandedSourceId = null;
				sourceCalls = [];
			}
		}
	}

	function findingsOf(report: ResearchReport | null): ResearchFinding[] {
		return report?.findings ?? [];
	}

	function certaintyClass(certainty: string): string {
		return certainty === 'fact' ? 'fact' : certainty === 'inference' ? 'inference' : 'assumption';
	}
</script>

<svelte:head>
	<title>Исследования — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Исследования</h1>
		<p class="subtitle">
			Ответы, проверенные по источникам. Фактом считается только вывод, который подтверждают
			минимум два независимых сайта; остальное помечено как предположение, а расхождения
			источников показаны отдельно.
		</p>
	</div>
	<button class="primary" on:click={() => (formOpen = !formOpen)} aria-expanded={formOpen}>
		<Search size={15} aria-hidden="true" /> Исследовать вопрос
	</button>
</header>

{#if formOpen}
	<form class="run-form" on:submit|preventDefault={investigate}>
		<label class="field">
			<span>Вопрос</span>
			<textarea
				bind:value={question}
				rows="2"
				placeholder="Например: какие ограничения у бесплатного тарифа Cloudflare Workers?"
				required
				maxlength="500"
			></textarea>
		</label>
		<label class="field">
			<span>Поисковые запросы <em>по одному на строку, необязательно</em></span>
			<textarea bind:value={queryText} rows="2" placeholder="Без запросов будет использован сам вопрос"></textarea>
		</label>
		<label class="checkbox">
			<input type="checkbox" bind:checked={saveKnowledge} />
			<span>Сохранить результат в базу знаний с источниками</span>
		</label>
		<div class="form-actions">
			<button type="button" class="ghost" on:click={() => (formOpen = false)}>Отмена</button>
			<button type="submit" class="primary" disabled={busy || !question.trim()}>
				{busy ? 'Ищу и читаю источники…' : 'Исследовать'}
			</button>
		</div>
		<p class="hint">
			Исследование читает страницы целиком и занимает до минуты. Журнал источников и уровни
			достоверности сохраняются вместе с результатом.
		</p>
	</form>
{/if}

<div class="tabs" role="tablist" aria-label="Разделы исследований">
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
{:else if tab === 'reports'}
	{#if reports.length === 0}
		<div class="empty">
			<Telescope size={28} strokeWidth={1.5} aria-hidden="true" />
			<p>
				Исследований пока нет. Задайте вопрос — LifeOS найдёт источники, прочитает их и покажет
				выводы со ссылками.
			</p>
		</div>
	{:else}
		<ul class="report-list">
			{#each reports as report (report.id)}
				<li class="report-card" class:expanded={expandedReportId === report.id}>
					<button
						class="card-head"
						on:click={() => toggleReport(report.id)}
						aria-expanded={expandedReportId === report.id}
					>
						<div class="title-row">
							<span class="title">{report.question}</span>
							<span class={`status-pill ${report.status}`}>{statusLabels[report.status] ?? report.status}</span>
						</div>
						<p class="meta">
							<span class="meta-item">выводов: {report.findings.length}</span>
							<span class="meta-item">источников: {report.sources}</span>
							<span class="meta-item">{formatDate(report.started_at)} {formatTime(report.started_at)}</span>
							{#if report.knowledge_entry_id}
								<a class="meta-link" href="/memory">в базе знаний</a>
							{/if}
						</p>
					</button>

					{#if expandedReportId === report.id}
						<div class="card-detail">
							{#if reportDetailLoading}
								<p class="empty" role="status">Загрузка…</p>
							{:else if reportDetail}
								{#if reportDetail.error}
									<p class="run-error">{reportDetail.error}</p>
								{/if}

								{#if reportDetail.status !== 'failed'}
									<section aria-label="Выводы">
									<h2>Выводы</h2>
									{#if findingsOf(reportDetail).length === 0}
										<p class="hint">
											Источники не дали проверяемых выводов: страницы не содержали фрагмента,
											отвечающего на вопрос. Это честный результат, а не сбой.
										</p>
									{:else}
										<ul class="findings">
											{#each findingsOf(reportDetail) as finding, index (index)}
												<li class={`finding ${certaintyClass(finding.certainty)}`}>
													<div class="finding-head">
														<span class={`certainty ${certaintyClass(finding.certainty)}`}>
															{certaintyLabels[finding.certainty] ?? finding.certainty}
														</span>
														{#if finding.certainty === 'fact' && finding.corroboration > 1}
															<span class="meta-item">
																подтверждают источников: {finding.corroboration}
															</span>
														{/if}
														<span class="meta-item">{Math.round(finding.confidence * 100)}% уверенности</span>
													</div>
													<p class="statement">{finding.statement}</p>
													{#if finding.conflict}
														<p class="conflict">
															<CircleAlert size={13} aria-hidden="true" /> {finding.conflict}
														</p>
													{/if}
													<ul class="citations">
														{#each finding.citations as citation, citationIndex (citationIndex)}
															<li>
																<a href={citation.url} target="_blank" rel="noreferrer noopener">
																	<ExternalLink size={12} aria-hidden="true" />
																	{citation.title || citation.site || citation.url}
																</a>
																<span class="citation-site">{citation.site}</span>
																{#if citation.read_unknown}
																	<span class="citation-warning">статус чтения неизвестен</span>
																{:else if !citation.read}
																	<span class="citation-warning">страница не прочитана</span>
																{/if}
															</li>
														{/each}
													</ul>
												</li>
											{/each}
										</ul>
									{/if}
									</section>
								{/if}

								{#if reportDetail.queries.length > 0}
									<section aria-label="Как проверялось">
										<h2>Как проверялось</h2>
										<p class="hint">Поисковые запросы: {reportDetail.queries.join('; ')}.</p>
									</section>
								{/if}

								<section aria-label="Итог">
									<h2>Итог</h2>
									<div class="summary">{@html renderMarkdown(reportDetail.summary)}</div>
								</section>

								<div class="actions">
									{#if reportDetail.knowledge_entry_id}
										<a class="ghost small" href="/memory">Открыть в памяти</a>
									{/if}
									<button class="ghost small danger" disabled={busy} on:click={() => removeReport(reportDetail!)}>
										<Trash2 size={13} aria-hidden="true" /> Удалить исследование
									</button>
								</div>
							{/if}
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
{:else if tab === 'watches'}
	<div class="section-head">
		<p class="hint">
			Наблюдение — это вопрос, который LifeOS перепроверяет по расписанию и о котором сообщает,
			только если ответ изменился.
		</p>
		<button class="ghost small" on:click={() => (watchFormOpen = !watchFormOpen)} aria-expanded={watchFormOpen}>
			<Plus size={13} aria-hidden="true" /> Наблюдение
		</button>
	</div>

	{#if watchFormOpen}
		<form class="run-form" on:submit|preventDefault={addWatch}>
			<label class="field">
				<span>Тема</span>
				<input type="text" bind:value={watchTopic} maxlength="200" placeholder="Например: релизы Go" required />
			</label>
			<label class="field">
				<span>Поисковый запрос</span>
				<input type="text" bind:value={watchQuery} maxlength="500" placeholder="go release notes" />
			</label>
			<label class="field">
				<span>Периодичность</span>
				<select bind:value={watchCadence}>
					{#each Object.entries(cadenceLabels) as [value, label] (value)}
						<option {value}>{label}</option>
					{/each}
				</select>
			</label>
			<div class="form-actions">
				<button type="button" class="ghost" on:click={() => (watchFormOpen = false)}>Отмена</button>
				<button type="submit" class="primary" disabled={busy || !watchTopic.trim()}>Создать</button>
			</div>
		</form>
	{/if}

	{#if watches.length === 0}
		<div class="empty">
			<Rss size={28} strokeWidth={1.5} aria-hidden="true" />
			<p>Наблюдений нет. Добавьте тему — LifeOS будет следить за ней и сообщать об изменениях.</p>
		</div>
	{:else}
		<ul class="watch-list">
			{#each watches as watch (watch.id)}
				<li class="watch-card" class:paused={!watch.enabled}>
					<div class="watch-main">
						<div class="title-row">
							<span class="title">{watch.topic}</span>
							<span class="cadence">{cadenceLabels[watch.cadence]}</span>
						</div>
						<p class="meta">
							<span class="meta-item mono">{watch.query}</span>
							{#if watch.last_checked_at}
								<span class="meta-item">проверено {formatDate(watch.last_checked_at)} {formatTime(watch.last_checked_at)}</span>
							{:else}
								<span class="meta-item">ещё не проверялось</span>
							{/if}
							{#if watch.next_check_at && watch.enabled}
								<span class="meta-item">дальше {formatDate(watch.next_check_at)} {formatTime(watch.next_check_at)}</span>
							{/if}
						</p>
						{#if watch.last_change_summary}
							<p class="change">
								<RefreshCw size={13} aria-hidden="true" />
								Изменения {formatDate(watch.last_change_at ?? null)}: {watch.last_change_summary}
							</p>
						{/if}
						{#if watchChange[watch.id]}
							<p class="change" role="status">{watchChange[watch.id]}</p>
						{/if}
					</div>
					<div class="watch-actions">
						<button class="ghost small" disabled={busy} on:click={() => checkWatch(watch)}>
							<RefreshCw size={13} aria-hidden="true" /> Проверить
						</button>
						{#if watch.enabled}
							<button class="ghost small" disabled={busy} on:click={() => setWatchEnabled(watch, false)}>
								<PauseCircle size={13} aria-hidden="true" /> Пауза
							</button>
						{:else}
							<button class="ghost small" disabled={busy} on:click={() => setWatchEnabled(watch, true)}>
								<Play size={13} aria-hidden="true" /> Включить
							</button>
						{/if}
						<button class="ghost small danger" disabled={busy} on:click={() => removeWatch(watch)}>
							<Trash2 size={13} aria-hidden="true" /> Удалить
						</button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
{:else if tab === 'sources'}
	<div class="section-head">
		<p class="hint">
			Источник — это сервис, к которому LifeOS обращается. Режим задаёте вы: только чтение,
			чтение и отправка изменений или синхронизация. Ассистент подключает источники выключенными
			и только на чтение.
		</p>
		<button class="ghost small" on:click={() => (sourceFormOpen = !sourceFormOpen)} aria-expanded={sourceFormOpen}>
			<Plus size={13} aria-hidden="true" /> Источник
		</button>
	</div>

	{#if sourceFormOpen}
		<form class="run-form" on:submit|preventDefault={addSource}>
			<div class="form-grid">
				<label class="field">
					<span>Название</span>
					<input type="text" bind:value={sourceName} maxlength="200" placeholder="GitHub API" required />
				</label>
				<label class="field">
					<span>Тип</span>
					<select bind:value={sourceKind}>
						{#each Object.entries(kindLabels) as [value, label] (value)}
							<option {value}>{label}</option>
						{/each}
					</select>
				</label>
			</div>
			<label class="field">
				<span>
					{sourceKind === 'web_search'
						? 'Шаблон запроса с {query}'
						: sourceKind === 'script'
							? 'Команда (например, hostname)'
							: 'Адрес'}
				</span>
				<input
					type="text"
					bind:value={sourceEndpoint}
					maxlength="2000"
					placeholder={sourceKind === 'web_search'
						? 'https://search.example.com/search?q={query}'
						: sourceKind === 'script'
							? 'hostname'
							: 'https://api.example.com/v1'}
					required
				/>
			</label>
			<div class="form-grid">
				<label class="field">
					<span>Режим</span>
					<select bind:value={sourceMode} disabled={sourceKind === 'script'}>
						{#each Object.entries(modeLabels) as [value, label] (value)}
							<option {value}>{label}</option>
						{/each}
					</select>
				</label>
				<label class="field">
					<span>Доверие</span>
					<select bind:value={sourceTrust}>
						{#each Object.entries(trustLabels) as [value, label] (value)}
							<option {value}>{label}</option>
						{/each}
					</select>
				</label>
			</div>
			<label class="field">
				<span>Описание <em>необязательно</em></span>
				<input type="text" bind:value={sourceNotes} maxlength="2000" placeholder="Зачем этот источник" />
			</label>
			<div class="form-actions">
				<button type="button" class="ghost" on:click={() => (sourceFormOpen = false)}>Отмена</button>
				<button type="submit" class="primary" disabled={busy || !sourceName.trim() || !sourceEndpoint.trim()}>
					Подключить
				</button>
			</div>
			<p class="hint">
				Скрипт запускается без оболочки: ввод из запроса подставляется одним аргументом, поэтому
				он не может стать второй командой.
			</p>
		</form>
	{/if}

	{#if sources.length === 0}
		<div class="empty">
			<Plug size={28} strokeWidth={1.5} aria-hidden="true" />
			<p>
				Внешних источников нет. Поиск в интернете уже работает через встроенного провайдера;
				источник нужен для конкретного сервиса — API, MCP-сервера или своего поиска.
			</p>
		</div>
	{:else}
		<ul class="source-list">
			{#each sources as source (source.id)}
				<li class="source-card" class:disabled={!source.enabled}>
					<button class="card-head" on:click={() => toggleSource(source)} aria-expanded={expandedSourceId === source.id}>
						<div class="title-row">
							<span class="title">
								{#if source.enabled}
									<ShieldCheck size={14} aria-hidden="true" />
								{:else}
									<ShieldOff size={14} aria-hidden="true" />
								{/if}
								{source.name}
							</span>
							<span class="source-kind">{kindLabels[source.kind]}</span>
						</div>
						<p class="meta">
							<span class="meta-item">{modeLabels[source.mode]}</span>
							<span class="meta-item">{trustLabels[source.trust]}</span>
							<span class="meta-item mono endpoint">{source.endpoint}</span>
							{#if source.last_used_at}
								<span class="meta-item">использован {formatDate(source.last_used_at)}</span>
							{/if}
							<span class={`status-pill ${source.enabled ? 'active' : 'paused'}`}>
								{source.enabled ? 'включён' : 'выключен'}
							</span>
						</p>
					</button>

					{#if expandedSourceId === source.id}
						<div class="card-detail">
							{#if source.notes}
								<p class="description">{source.notes}</p>
							{/if}
							<div class="actions">
								{#if source.enabled}
									<button class="ghost small" disabled={busy} on:click={() => setSourceEnabled(source, false)}>
										<ShieldOff size={13} aria-hidden="true" /> Выключить
									</button>
								{:else}
									<button class="ghost small" disabled={busy} on:click={() => setSourceEnabled(source, true)}>
										<ShieldCheck size={13} aria-hidden="true" /> Включить
									</button>
								{/if}
								<label class="inline-field">
									<span>Режим</span>
									<select
										value={source.mode}
										disabled={busy || source.kind === 'script'}
										on:change={(event) =>
											setSourceMode(source, (event.currentTarget as HTMLSelectElement).value as ResearchAccessMode)}
									>
										{#each Object.entries(modeLabels) as [value, label] (value)}
											<option {value}>{label}</option>
										{/each}
									</select>
								</label>
								{#if source.kind === 'mcp'}
									<label class="inline-field tools-field">
										<span>Инструменты для чтения</span>
										<input
											type="text"
											value={(source.allowed_tools ?? []).join(', ')}
											placeholder="read_repo, list_issues"
											disabled={busy}
											on:change={(event) =>
												saveAllowedTools(source, (event.currentTarget as HTMLInputElement).value)}
										/>
									</label>
								{/if}
								{#if source.enabled}
									<button class="ghost small" disabled={busy} on:click={() => testSource(source)}>
										<Globe size={13} aria-hidden="true" /> Проверить связь
									</button>
								{/if}
								<button class="ghost small danger" disabled={busy} on:click={() => removeSource(source)}>
									<Trash2 size={13} aria-hidden="true" /> Удалить
								</button>
							</div>

							{#if source.kind === 'mcp'}
								<p class="hint">
									Без изменения данных выполняются только инструменты из списка, и список
									составляете вы. Пометка сервера «только чтение» — его собственное заявление,
									LifeOS не считает её гарантией: инструмент без вашей записи требует
									изменяющего вызова с подтверждением.
								</p>
							{/if}

							<section aria-label="Журнал обращений">
								<h2>Журнал обращений</h2>
								{#if sourceCalls.length === 0}
									<p class="hint">Обращений ещё не было.</p>
								{:else}
									<ul class="calls">
										{#each sourceCalls as call (call.id)}
											<li class="call">
												<span class={`outcome ${call.outcome}`}>{outcomeLabels[call.outcome] ?? call.outcome}</span>
												<span class="call-op mono">
													{call.method ? `${call.method} ` : ''}{call.operation || '—'}
												</span>
												<span class="call-when">{formatDate(call.created_at)} {formatTime(call.created_at)}</span>
												{#if call.detail}
													<span class="call-detail">{call.detail}</span>
												{/if}
											</li>
										{/each}
									</ul>
								{/if}
							</section>
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
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

	.primary {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: var(--radius-md);
		padding: 0.55rem 0.9rem;
		font: inherit;
		font-size: var(--text-md);
		font-weight: 600;
		cursor: pointer;
	}

	.ghost {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		background: transparent;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.45rem 0.8rem;
		font: inherit;
		font-size: var(--text-base);
		cursor: pointer;
		text-decoration: none;
	}

	.ghost:hover {
		background: var(--bg-raised);
	}

	.ghost.danger {
		color: var(--danger-text);
		border-color: var(--danger-border);
	}

	.primary.small,
	.ghost.small {
		padding: 0.3rem 0.6rem;
		font-size: var(--text-sm);
		min-height: 32px;
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
		min-height: 44px;
		padding: var(--space-2) var(--space-3);
		border: none;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--fg-muted);
		font: inherit;
		font-size: var(--text-base);
		cursor: pointer;
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
		margin-bottom: var(--space-3);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-md);
		font-size: var(--text-base);
	}

	.banner {
		border: 1px solid var(--danger-border);
		background: var(--danger-surface);
		color: var(--danger-text);
	}

	.notice {
		border: 1px solid var(--success-surface);
		background: var(--success-surface);
		color: var(--fg-primary);
	}

	.banner button,
	.notice button {
		border: none;
		background: transparent;
		color: inherit;
		font-size: 1.1rem;
		cursor: pointer;
	}

	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-10) var(--space-4);
		color: var(--fg-muted);
		text-align: center;
	}

	.empty p {
		margin: 0;
		max-width: 46ch;
		font-size: var(--text-md);
	}

	.run-form {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--bg-card);
	}

	.form-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-3);
	}

	.field {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		min-width: 0;
	}

	.field > span {
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.field em {
		color: var(--fg-faint);
		font-style: normal;
	}

	.field input,
	.field textarea,
	.field select,
	.inline-field select,
	.inline-field input {
		width: 100%;
	}

	.tools-field {
		flex-basis: 100%;
	}

	.inline-field select,
	.inline-field input {
		padding: 0.45rem 0.6rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		color: var(--fg-primary);
		font: inherit;
		font-size: var(--text-base);
	}

	.inline-field input {
		min-height: 44px;
	}

	.checkbox {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-body);
		font-size: var(--text-base);
	}

	.form-actions {
		display: flex;
		justify-content: flex-end;
		gap: var(--space-2);
	}

	.hint {
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-sm);
		max-width: 72ch;
	}

	.section-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3);
		margin-bottom: var(--space-3);
	}

	.report-list,
	.watch-list,
	.source-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.report-card,
	.source-card,
	.watch-card {
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--bg-card);
		overflow: hidden;
	}

	.report-card.expanded,
	.source-card.expanded {
		border-color: var(--accent-border);
	}

	.card-head {
		display: flex;
		width: 100%;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3) var(--space-4);
		border: none;
		background: transparent;
		color: inherit;
		font: inherit;
		text-align: left;
		cursor: pointer;
	}

	.card-head:hover {
		background: var(--bg-hover);
	}

	.title-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.title {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-primary);
		font-size: var(--text-lg);
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.meta-item {
		font-variant-numeric: tabular-nums;
	}

	.mono {
		font-family: var(--font-mono, 'JetBrains Mono', ui-monospace, monospace);
		font-size: var(--text-xs);
	}

	.endpoint {
		max-width: 36ch;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.meta-link {
		color: var(--accent-bright);
		text-decoration: none;
		font-size: var(--text-sm);
	}

	.meta-link:hover {
		text-decoration: underline;
	}

	.status-pill {
		padding: 0.1rem 0.5rem;
		border-radius: var(--radius-full);
		border: 1px solid var(--border);
		color: var(--fg-muted);
		font-size: var(--text-xs);
		white-space: nowrap;
	}

	.status-pill.completed,
	.status-pill.active {
		border-color: var(--success-surface);
		color: var(--success);
	}

	.status-pill.failed {
		border-color: var(--danger-border);
		color: var(--danger-text);
	}

	.status-pill.paused {
		color: var(--fg-faint);
	}

	.source-kind,
	.cadence {
		color: var(--fg-muted);
		font-size: var(--text-xs);
		letter-spacing: 0.03em;
	}

	.card-detail {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: 0 var(--space-4) var(--space-4);
		border-top: 1px solid var(--border-soft);
	}

	.description {
		margin: var(--space-3) 0 0;
		color: var(--fg-body);
		font-size: var(--text-md);
		max-width: 72ch;
	}

	.findings {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.finding {
		padding: var(--space-3);
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
	}

	.finding.fact {
		border-color: var(--success-surface);
	}

	.finding.assumption {
		border-color: var(--warning-border);
	}

	.finding-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.certainty {
		padding: 0.05rem 0.45rem;
		border-radius: var(--radius-full);
		border: 1px solid var(--border);
		font-size: var(--text-xs);
	}

	.certainty.fact {
		border-color: var(--success-surface);
		color: var(--success);
	}

	.certainty.assumption {
		border-color: var(--warning-border);
		color: var(--warning);
	}

	.certainty.inference {
		color: var(--fg-muted);
	}

	.statement {
		margin: 0;
		color: var(--fg-body);
		font-size: var(--text-md);
		line-height: 1.55;
		max-width: 72ch;
	}

	.conflict {
		display: flex;
		align-items: flex-start;
		gap: var(--space-2);
		margin: var(--space-2) 0 0;
		color: var(--warning);
		font-size: var(--text-sm);
	}

	.citations {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		margin: var(--space-2) 0 0;
		padding: 0;
		list-style: none;
		font-size: var(--text-sm);
	}

	.citations li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		color: var(--fg-muted);
	}

	.citations a {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--accent-bright);
		text-decoration: none;
		max-width: 34ch;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.citations a:hover {
		text-decoration: underline;
	}

	.citation-site {
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.citation-warning {
		color: var(--warning);
		font-size: var(--text-xs);
	}

	.summary {
		color: var(--fg-body);
		font-size: var(--text-base);
		line-height: 1.6;
		max-width: 78ch;
	}

	.summary :global(h1),
	.summary :global(h2) {
		margin: var(--space-4) 0 var(--space-1);
		font-size: var(--text-lg);
		font-weight: 600;
		text-transform: none;
		letter-spacing: -0.01em;
		color: var(--fg-primary);
	}

	.summary :global(h1) {
		margin-top: 0;
	}

	/* The first heading repeats the question the card already shows. */
	.summary :global(h1:first-child),
	.summary :global(h2:first-child) {
		display: none;
	}

	.summary :global(h3) {
		margin: var(--space-3) 0 var(--space-1);
		font-size: var(--text-base);
		font-weight: 600;
		text-transform: none;
		letter-spacing: 0;
		color: var(--fg-primary);
	}

	.summary :global(p) {
		margin: var(--space-2) 0;
	}

	.summary :global(ul),
	.summary :global(ol) {
		margin: var(--space-1) 0;
		padding-left: var(--space-4);
	}

	.summary :global(li) {
		margin: var(--space-1) 0;
	}

	.summary :global(a) {
		color: var(--accent-bright);
	}

	.summary :global(code) {
		font-family: var(--font-mono, 'JetBrains Mono', ui-monospace, monospace);
		font-size: var(--text-sm);
	}

	.actions,
	.watch-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}

	.watch-card {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
	}

	.watch-card.paused {
		opacity: 0.75;
	}

	.watch-main {
		display: flex;
		min-width: 0;
		flex-direction: column;
		gap: var(--space-1);
	}

	.change {
		display: flex;
		align-items: center;
		gap: var(--space-1);
		margin: var(--space-1) 0 0;
		color: var(--accent-bright);
		font-size: var(--text-sm);
	}

	.run-error {
		margin: var(--space-3) 0 0;
		padding: var(--space-2) var(--space-3);
		border: 1px solid var(--danger-border);
		border-radius: var(--radius-md);
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-sm);
	}

	.calls {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.call {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-1) 0;
		border-bottom: 1px solid var(--border-soft);
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.outcome {
		padding: 0.05rem 0.4rem;
		border-radius: var(--radius-full);
		border: 1px solid var(--border);
		font-size: var(--text-xs);
	}

	.outcome.ok {
		color: var(--success);
		border-color: var(--success-surface);
	}

	.outcome.refused,
	.outcome.unknown {
		color: var(--warning);
		border-color: var(--warning-border);
	}

	.outcome.started {
		color: var(--fg-muted);
		border-style: dashed;
	}

	.outcome.failed {
		color: var(--danger-text);
		border-color: var(--danger-border);
	}

	.call-op {
		color: var(--fg-body);
	}

	.call-detail {
		flex: 1 1 100%;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.inline-field {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.inline-field select {
		width: auto;
		padding: 0.2rem 0.4rem;
		font-size: var(--text-sm);
	}

	@media (max-width: 820px) {
		.page-header {
			flex-direction: column;
			align-items: stretch;
		}

		/* Touch targets grow on the narrow screen the same rows are tapped on. */
		.primary.small,
		.ghost.small {
			min-height: 44px;
		}

		.form-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.watch-card {
			flex-direction: column;
		}

		.endpoint {
			max-width: 100%;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.card-head,
		.ghost,
		.primary {
			transition: none;
		}
	}
</style>
