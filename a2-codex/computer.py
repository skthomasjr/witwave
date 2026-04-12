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

    A single browser + page is lazily created on first use and reused across
    tool calls within a session. Call `close()` to release resources.
    """

    def __init__(self, width: int = 1280, height: int = 800) -> None:
        self._width = width
        self._height = height
        self._playwright = None
        self._browser = None
        self._context = None
        self._page = None
        self._lock = asyncio.Lock()

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
            from playwright.async_api import async_playwright
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

    async def screenshot(self) -> str:
        await self._ensure_page()
        png = await self._page.screenshot(type="png")
        return base64.b64encode(png).decode()

    async def click(self, x: int, y: int, button: Button) -> None:
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
        await self._ensure_page()
        await self._page.mouse.dblclick(x, y)

    async def scroll(self, x: int, y: int, scroll_x: int, scroll_y: int) -> None:
        await self._ensure_page()
        await self._page.mouse.move(x, y)
        await self._page.evaluate(f"window.scrollBy({scroll_x}, {scroll_y})")

    async def type(self, text: str) -> None:
        await self._ensure_page()
        await self._page.keyboard.type(text)

    async def wait(self) -> None:
        await asyncio.sleep(1.0)

    async def move(self, x: int, y: int) -> None:
        await self._ensure_page()
        await self._page.mouse.move(x, y)

    async def keypress(self, keys: list[str]) -> None:
        await self._ensure_page()
        for key in keys:
            await self._page.keyboard.press(key)

    async def drag(self, path: list[tuple[int, int]]) -> None:
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
            await self._context.close()
            self._context = None
            self._page = None
        if self._browser:
            await self._browser.close()
            self._browser = None
        if self._playwright:
            await self._playwright.stop()
            self._playwright = None
        logger.info("Playwright browser closed")
