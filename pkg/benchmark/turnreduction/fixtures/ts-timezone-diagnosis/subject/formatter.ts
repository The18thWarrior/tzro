export function formatDate(date: Date, tz: string): string {
    const sign = tz.startsWith('-') ? -1 : 1;
    const hours = parseInt(tz.substring(1, 3), 10);
    const localTime = new Date(date.getTime() - (sign * hours * 60 * 60 * 1000)); 
    return localTime.toISOString().replace('Z', '') + tz;
}
