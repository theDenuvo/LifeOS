import { writable } from 'svelte/store';
import type { CalendarName } from './api';

export const allCalendars: CalendarName[] = ['work', 'meetings', 'personal'];

// The sidebar calendar sources act as visibility filters. The calendar fetches
// all events and filters locally so toggling is instant and can hide every
// source without a special "empty" request.
export const selectedCalendars = writable<CalendarName[]>([...allCalendars]);

export function toggleCalendar(name: CalendarName): void {
	selectedCalendars.update((current) =>
		current.includes(name) ? current.filter((item) => item !== name) : [...current, name]
	);
}
