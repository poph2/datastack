export default function Home() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 text-center font-sans dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        DataStack
      </h1>
      <p className="max-w-md text-sm text-zinc-600 dark:text-zinc-400">
        Live command center shell. Pipeline status, chat, and dashboards land
        here next.
      </p>
    </main>
  );
}
