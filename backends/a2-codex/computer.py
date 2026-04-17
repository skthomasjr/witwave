"""Playwright-backed AsyncComputer implementation for ComputerTool."""
import asyncio
import base64
import logging

from agents.computer import AsyncComputer, Button

logger = logging.getLogger(__name__)

_BUTTON_MAP: dict[str, str] = {
    "left": "left",
    "right": "right",
    "wheel": "middle",
}


class PlaywrightComputer(AsyncComputer):
    """AsyncComputer backed by a headless Playwright Chromium browser.

    Two usage modes are supported:

    1. **Stand-alone mode** (``__init__`` with no ``pool``): the instance
       manages its own Playwright process, browser, context, and page. This
       preserves the original single-session behaviour and is used for
       simple/test paths.

    2. **Pool-scoped mode** (``__init__`` with a ``BrowserPool`` and
       ``session_id``): the instance shares the pool's long-lived browser
       process but owns a fresh ``BrowserContext`` + ``Page`` that are
       isolated from every other session (cookies, localStorage,
       service workers, cache, IndexedDB). Call ``close()`` to release the
       context; the pool's browser stays up for the process lifetime.

    Either way, ``_op_lock`` serialises page operations for one computer
    instance. Per-session instances do not share a lock — the Agents SDK
    handles one session at a time per ComputerTool binding.
    """

    def __init__(
        self,
        width: int = 1280,
        height: int = 800,
        *,
        pool: "BrowserPool | None" = None,
        session_id: str | None = None,
    ) -> None:
        self._width = width
        self._height = height
        self._playwright = None
        self._browser = None
        self._context = None
        self._page = None
        self._pool = pool
        self._session_id = session_id
        self._lock = asyncio.Lock()
        # Serializes page operations across concurrent callers.  All public
        # methods that interact with self._page must be called while holding
        # this lock so that concurrent sessions do not interleave page actions.
        self._op_lock = asyncio.Lock()

    @property
    def environment(self):
        return "browser"

    @property
    def dimensions(self) -> tuple[int, int]:
        return (self._width, self._height)

    async def _ensure_page(self):
        async with self._lock:
            if self._page is not None:
                return
            if self._pool is not None:
                # Pool-scoped: borrow the shared browser, open a private
                # context + page for this session.
                browser = await self._pool._ensure_browser()
                self._context = await browser.new_context(
                    viewport={"width": self._width, "height": self._height},
                )
                self._page = await self._context.new_page()
                logger.info(
                    "Playwright per-session context opened for session %r (%dx%d)",
                    self._session_id, self._width, self._height,
                )
                return
            # Stand-alone: launch an independent browser process.
            from playwright.async_api import async_playwright
            try:
                self._playwright = await async_playwright().start()
                self._browser = await self._playwright.chromium.launch(
                    headless=True,
                    args=["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
                )
                self._context = await self._browser.new_context(
                    viewport={"width": self._width, "height": self._height},
                )
                self._page = await self._context.new_page()
                logger.info("Playwright browser started (%dx%d)", self._width, self._height)
            except Exception:
                # Clean up any partially-initialized resources so the next call
                # can retry from a clean state without leaking Playwright processes.
                await self.close()
                raise

    async def screenshot(self) -> str:
        async with self._op_lock:
            await self._ensure_page()
            png = await self._page.screenshot(type="png")
            return base64.b64encode(png).decode()

    async def click(self, x: int, y: int, button: Button) -> None:
        async with self._op_lock:
            await self._ensure_page()
            if button == "back":
                await self._page.go_back()
                return
            if button == "forward":
                await self._page.go_forward()
                return
            pw_button = _BUTTON_MAP.get(button, "left")
            await self._page.mouse.click(x, y, button=pw_button)

    async def double_click(self, x: int, y: int) -> None:
        async with self._op_lock:
            await self._ensure_page()
            await self._page.mouse.dblclick(x, y)

    async def scroll(self, x: int, y: int, scroll_x: int, scroll_y: int) -> None:
        async with self._op_lock:
            await self._ensure_page()
            await self._page.mouse.move(x, y)
            await self._page.evaluate(f"window.scrollBy({scroll_x}, {scroll_y})")

    async def type(self, text: str) -> None:
        async with self._op_lock:
            await self._ensure_page()
            await self._page.keyboard.type(text)

    async def wait(self) -> None:
        await asyncio.sleep(1.0)

    async def move(self, x: int, y: int) -> None:
        async with self._op_lock:
            await self._ensure_page()
            await self._page.mouse.move(x, y)

    async def keypress(self, keys: list[str]) -> None:
        async with self._op_lock:
            await self._ensure_page()
            for key in keys:
                await self._page.keyboard.press(key)

    async def drag(self, path: list[tuple[int, int]]) -> None:
        async with self._op_lock:
            await self._ensure_page()
            if not path:
                return
            await self._page.mouse.move(path[0][0], path[0][1])
            await self._page.mouse.down()
            for x, y in path[1:]:
                await self._page.mouse.move(x, y)
            await self._page.mouse.up()

    async def close(self) -> None:
        if self._context:
            try:
                await self._context.close()
            except Exception as _e:
                logger.warning("Error closing Playwright context: %s", _e)
            self._context = None
            self._page = None
        # Only tear down the browser + playwright process if this instance
        # owns them (stand-alone mode). Pool-scoped instances leave the
        # shared browser alone so other sessions can keep using it.
        if self._pool is None:
            if self._browser:
                try:
                    await self._browser.close()
                except Exception as _e:
                    logger.warning("Error closing Playwright browser: %s", _e)
                self._browser = None
            if self._playwright:
                try:
                    await self._playwright.stop()
                except Exception as _e:
                    logger.warning("Error stopping Playwright: %s", _e)
                self._playwright = None
            logger.info("Playwright browser closed")
        else:
            logger.info(
                "Playwright per-session context closed for session %r",
                self._session_id,
            )


class BrowserPool:
    """Shared Playwright browser process with per-session context scoping.

    One Chromium process is launched lazily on first use and reused for the
    lifetime of the pool. Each call to ``acquire(session_id)`` returns a
    ``PlaywrightComputer`` backed by a fresh ``BrowserContext`` — so
    cookies, localStorage, service workers, cache, IndexedDB, and page
    state are isolated between sessions.

    Call ``release(session_id)`` (or ``close()`` for the whole pool) to
    close the per-session context. The shared browser keeps running until
    ``close()`` is invoked.

    Addresses #522: the previous module-global ``PlaywrightComputer``
    singleton shared one browser context across every A2A session, which
    leaked authenticated state and page contents between trust boundaries.
    """

    def __init__(self, width: int = 1280, height: int = 800) -> None:
        self._width = width
        self._height = height
        self._playwright = None
        self._browser = None
        self._browser_lock = asyncio.Lock()
        self._computers: dict[str, PlaywrightComputer] = {}
        self._computers_lock = asyncio.Lock()

    async def _ensure_browser(self):
        async with self._browser_lock:
            if self._browser is not None:
                return self._browser
            from playwright.async_api import async_playwright
            try:
                self._playwright = await async_playwright().start()
                self._browser = await self._playwright.chromium.launch(
                    headless=True,
                    args=["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
                )
                logger.info(
                    "Playwright shared browser started (%dx%d) for per-session contexts",
                    self._width, self._height,
                )
                return self._browser
            except Exception:
                # Clean partial state so a retry can restart cleanly.
                if self._browser is not None:
                    try:
                        await self._browser.close()
                    except Exception:
                        pass
                    self._browser = None
                if self._playwright is not None:
                    try:
                        await self._playwright.stop()
                    except Exception:
                        pass
                    self._playwright = None
                raise

    async def acquire(self, session_id: str) -> PlaywrightComputer:
        """Return a session-scoped PlaywrightComputer.

        A single computer instance is cached per ``session_id`` for the
        lifetime of the session so consecutive tool calls within one
        session reuse their page/history (matching the semantics the
        original docstring claimed but did not enforce).
        """
        async with self._computers_lock:
            existing = self._computers.get(session_id)
            if existing is not None:
                return existing
            computer = PlaywrightComputer(
                width=self._width,
                height=self._height,
                pool=self,
                session_id=session_id,
            )
            self._computers[session_id] = computer
            return computer

    async def release(self, session_id: str) -> None:
        """Close and drop the per-session context for ``session_id``.

        Safe to call for sessions that never acquired a computer.
        """
        async with self._computers_lock:
            computer = self._computers.pop(session_id, None)
        if computer is None:
            return
        try:
            await computer.close()
        except Exception as _e:
            logger.warning(
                "Failed to close per-session PlaywrightComputer for %r: %s",
                session_id, _e,
            )

    async def close(self) -> None:
        """Close every per-session context and the shared browser."""
        async with self._computers_lock:
            computers = list(self._computers.items())
            self._computers.clear()
        for session_id, computer in computers:
            try:
                await computer.close()
            except Exception as _e:
                logger.warning(
                    "Failed to close per-session PlaywrightComputer for %r on shutdown: %s",
                    session_id, _e,
                )
        async with self._browser_lock:
            if self._browser is not None:
                try:
                    await self._browser.close()
                except Exception as _e:
                    logger.warning("Error closing shared Playwright browser: %s", _e)
                self._browser = None
            if self._playwright is not None:
                try:
                    await self._playwright.stop()
                except Exception as _e:
                    logger.warning("Error stopping shared Playwright: %s", _e)
                self._playwright = None
        logger.info("Playwright shared browser pool closed")
