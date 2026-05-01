// Async-iterable queue used to feed user turns into the SDK's prompt input.
export class PromptStream implements AsyncIterable<string> {
  private queue: string[] = [];
  private waiters: Array<(turn: IteratorResult<string>) => void> = [];
  private closed = false;

  push(turn: string): void {
    if (this.closed) throw new Error("PromptStream is closed");
    const w = this.waiters.shift();
    if (w) {
      w({ value: turn, done: false });
    } else {
      this.queue.push(turn);
    }
  }

  close(): void {
    this.closed = true;
    for (const w of this.waiters) w({ value: undefined, done: true });
    this.waiters.length = 0;
  }

  [Symbol.asyncIterator](): AsyncIterator<string> {
    return {
      next: (): Promise<IteratorResult<string>> => {
        const turn = this.queue.shift();
        if (turn !== undefined) {
          return Promise.resolve({ value: turn, done: false });
        }
        if (this.closed) {
          return Promise.resolve({ value: undefined as unknown as string, done: true });
        }
        return new Promise((resolve) => this.waiters.push(resolve));
      },
    };
  }
}
