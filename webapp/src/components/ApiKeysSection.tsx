import React, { useEffect, useState } from 'react'
import { Button, CardLayout } from './elements'
import {
    API_LIST_API_KEYS,
    API_CREATE_API_KEY,
    API_REVOKE_API_KEY,
    IAPIKey,
    API_CREATE_API_KEY_RESP,
} from '../api'

// One-shot reveal modal. Plaintext is shown EXACTLY once at creation time;
// the server only stores a SHA-256 hash, so a lost key cannot be recovered.
const NewKeyReveal: React.FC<{ plaintext: string; onDismiss: () => void }> = ({ plaintext, onDismiss }) => {
    const [copied, setCopied] = useState(false)
    const copy = () => {
        navigator.clipboard.writeText(plaintext).then(() => setCopied(true))
    }
    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50">
            <div className="bg-white rounded shadow-lg p-6 max-w-lg w-full m-4">
                <h3 className="text-lg font-bold mb-2">Save your API key now</h3>
                <p className="text-sm mb-3">
                    This key will not be shown again. If you lose it, revoke it and create a new one.
                </p>
                <div className="bg-gray-100 border rounded p-2 font-mono text-xs break-all select-all">
                    {plaintext}
                </div>
                <div className="flex flex-row items-center mt-3 space-x-2">
                    <Button button secondary small title={copied ? 'Copied' : 'Copy'} handleOnClick={copy} />
                    <Button button primary small title="I have saved it" handleOnClick={onDismiss} />
                </div>
            </div>
        </div>
    )
}

const formatDate = (s?: string) => {
    if (!s) return 'never'
    try {
        return new Date(s).toLocaleString()
    } catch {
        return s
    }
}

const ApiKeysSection: React.FC = () => {
    const [keys, setKeys] = useState<IAPIKey[]>([])
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState<string | null>(null)
    const [newName, setNewName] = useState('')
    const [creating, setCreating] = useState(false)
    const [revealing, setRevealing] = useState<string | null>(null)
    const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null)

    const refresh = async () => {
        setLoading(true)
        setError(null)
        try {
            const resp = await API_LIST_API_KEYS()
            if (!resp.ok) {
                setError('Failed to load API keys.')
                return
            }
            const data: IAPIKey[] = await resp.json()
            setKeys(data || [])
        } catch (e) {
            setError('Network error loading API keys.')
        } finally {
            setLoading(false)
        }
    }

    useEffect(() => {
        refresh()
    }, [])

    const onCreate = async (e: React.FormEvent) => {
        e.preventDefault()
        if (!newName.trim()) return
        setCreating(true)
        setError(null)
        try {
            const resp = await API_CREATE_API_KEY(newName.trim())
            if (!resp.ok) {
                setError('Failed to create key.')
                return
            }
            const data: API_CREATE_API_KEY_RESP = await resp.json()
            setRevealing(data.plaintext)
            setNewName('')
            await refresh()
        } catch (e) {
            setError('Network error creating key.')
        } finally {
            setCreating(false)
        }
    }

    const onRevoke = async (id: string) => {
        setError(null)
        try {
            const resp = await API_REVOKE_API_KEY(id)
            if (!resp.ok) {
                setError('Failed to revoke key.')
                return
            }
            await refresh()
        } catch (e) {
            setError('Network error revoking key.')
        } finally {
            setConfirmRevoke(null)
        }
    }

    return (
        <CardLayout title="API keys">
            <p className="mb-2">
                API keys authenticate scripts, MCP clients, and other automation against your account.
                Each key acts as your account when sent via <code>Authorization: Bearer &lt;key&gt;</code>.
                Workspace selection is passed per request (or per MCP tool call).
            </p>
            <p className="mb-2 text-xs text-gray-600">
                Keys are stored hashed; the full key is shown only once at creation. Revoke any key
                you no longer need or that may be compromised.
            </p>

            <hr />

            <form onSubmit={onCreate} className="flex flex-row items-center my-2">
                <input
                    type="text"
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    placeholder="Key name (e.g. local-mcp, my-laptop)"
                    className="rounded p-1 border flex-grow mr-2"
                    maxLength={80}
                />
                <Button submit primary small title={creating ? 'Creating...' : 'Create key'} />
            </form>

            {error && <div className="p-1 text-red-500 text-xs font-bold">{error}</div>}

            {loading ? (
                <div className="p-2 text-xs">Loading...</div>
            ) : keys.length === 0 ? (
                <div className="p-2 text-xs text-gray-600">No API keys yet. Create one above.</div>
            ) : (
                <table className="w-full text-xs mt-2">
                    <thead>
                        <tr className="text-left border-b">
                            <th className="p-1">Name</th>
                            <th className="p-1">Prefix</th>
                            <th className="p-1">Created</th>
                            <th className="p-1">Last used</th>
                            <th className="p-1"></th>
                        </tr>
                    </thead>
                    <tbody>
                        {keys.map((k) => (
                            <tr key={k.id} className="border-b">
                                <td className="p-1 font-medium">{k.name}</td>
                                <td className="p-1 font-mono">{k.keyPrefix}...</td>
                                <td className="p-1">{formatDate(k.createdAt)}</td>
                                <td className="p-1">{formatDate(k.lastUsedAt)}</td>
                                <td className="p-1 text-right">
                                    {confirmRevoke === k.id ? (
                                        <span className="space-x-1">
                                            <Button
                                                button
                                                warning
                                                small
                                                title="Confirm revoke"
                                                handleOnClick={() => onRevoke(k.id)}
                                            />
                                            <Button
                                                button
                                                secondary
                                                small
                                                title="Cancel"
                                                handleOnClick={() => setConfirmRevoke(null)}
                                            />
                                        </span>
                                    ) : (
                                        <Button
                                            button
                                            secondary
                                            small
                                            title="Revoke"
                                            handleOnClick={() => setConfirmRevoke(k.id)}
                                        />
                                    )}
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            )}

            {revealing && (
                <NewKeyReveal plaintext={revealing} onDismiss={() => setRevealing(null)} />
            )}
        </CardLayout>
    )
}

export default ApiKeysSection
