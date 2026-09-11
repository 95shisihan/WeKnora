// Retry only transient transport failures, and only for explicitly opted-in reads.
export async function retryRead<T>(read: () => Promise<T>): Promise<T> {
  try {
    return await read()
  } catch (error) {
    const code = (error as { code?: string })?.code
    if (!['ERR_NETWORK', 'ECONNABORTED', 'ETIMEDOUT', 'ECONNRESET'].includes(code ?? '')) {
      throw error
    }
    await new Promise((resolve) => setTimeout(resolve, 300))
    return read()
  }
}
