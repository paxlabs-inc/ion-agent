import asyncio
import json
import sys

from cua_driver import (
    CaptureScope,
    ClickButton,
    ClickInput,
    CuaDriver,
    DesktopScope,
    DriverOptions,
    EndSessionInput,
    GetDesktopStateInput,
    GetScreenSizeInput,
    HotkeyInput,
    MoveCursorInput,
    PressKeyInput,
    ScrollBy,
    ScrollDirection,
    ScrollInput,
    StartSessionInput,
    TypeTextInput,
)


class Bridge:
    def __init__(self) -> None:
        self.session = "ion-private-desktop"
        self.agent_session = "ion-private-agent"
        self.driver = CuaDriver.create(
            DriverOptions(claude_code_compatibility=False)
        )

    async def start(self) -> None:
        await self.driver.start_session(
            StartSessionInput(
                session=self.session,
                capture_scope=CaptureScope.DESKTOP,
            )
        )
        await self.driver.start_session(
            StartSessionInput(
                session=self.agent_session,
                capture_scope=CaptureScope.AUTO,
            )
        )

    async def close(self) -> None:
        try:
            await self.driver.end_session(
                EndSessionInput(session=self.agent_session)
            )
            await self.driver.end_session(EndSessionInput(session=self.session))
        finally:
            await self.driver.shutdown()

    async def dispatch(self, method: str, params: dict) -> dict:
        if method == "probe":
            metadata = await self.driver.metadata()
            size = await self.driver.get_screen_size(
                GetScreenSizeInput(session=self.session)
            )
            self._require_success(size)
            return {
                "driver_version": metadata.driver_version,
                "contract_version": metadata.contract_version,
                "embedded": metadata.embedded,
                "screen": json.loads(size.raw_json),
            }
        if method == "frame":
            frame = await self.driver.get_desktop_state(
                GetDesktopStateInput(
                    session=self.session,
                    screenshot_out_file=None,
                )
            )
            self._require_success(frame)
            if len(frame.images) != 1:
                raise RuntimeError("Cua returned an invalid desktop frame")
            image = frame.images[0]
            return {
                "mime_type": image.mime_type,
                "data_base64": image.data_base64,
                "degraded": frame.degraded,
                "verified": frame.verified,
            }
        if method == "observe":
            if int(params.get("pid", 0)) == 0:
                result = await self.driver.call_tool(
                    "list_windows",
                    json.dumps(
                        {"on_screen_only": True},
                        separators=(",", ":"),
                    ),
                )
            else:
                arguments = {
                    "pid": int(params["pid"]),
                    "window_id": int(params["window_id"]),
                    "include_screenshot": False,
                    "max_elements": int(params.get("max_elements", 200)),
                    "max_depth": int(params.get("max_depth", 20)),
                    "session": self.agent_session,
                }
                if params.get("query"):
                    arguments["query"] = str(params["query"])
                result = await self.driver.call_tool(
                    "get_window_state",
                    json.dumps(arguments, separators=(",", ":")),
                )
            self._require_success(result)
            observation = json.loads(result.raw_json)
            if int(params.get("pid", 0)) != 0:
                binding = await self.driver.call_tool(
                    "get_browser_state",
                    json.dumps(
                        {
                            "pid": int(params["pid"]),
                            "window_id": int(params["window_id"]),
                            "session": self.agent_session,
                        },
                        separators=(",", ":"),
                    ),
                )
                if not binding.is_error:
                    binding_value = json.loads(binding.raw_json)
                    structured = binding_value.get("structuredContent", {})
                    tabs = structured.get("tabs", [])
                    active = next(
                        (tab for tab in tabs if tab.get("active")),
                        tabs[0] if tabs else None,
                    )
                    if active is not None:
                        snapshot_arguments = {
                            "target_id": structured["target_id"],
                            "tab_id": active["tab_id"],
                            "session": self.agent_session,
                            "snapshot_format": "semantic_v2",
                            "include_screenshot": False,
                        }
                        if params.get("query"):
                            snapshot_arguments["query"] = str(params["query"])
                        snapshot = await self.driver.call_tool(
                            "get_browser_state",
                            json.dumps(
                                snapshot_arguments,
                                separators=(",", ":"),
                            ),
                        )
                        if not snapshot.is_error:
                            observation["browser"] = self._bound(
                                json.loads(snapshot.raw_json)
                            )
            return self._bound(observation)
        if method == "window_input":
            tool = str(params["kind"])
            if (
                params.get("target_id")
                and params.get("tab_id")
                and tool in {"click", "type_text", "browser_navigate"}
            ):
                browser_tools = {
                    "click": "browser_click",
                    "type_text": "browser_type",
                    "browser_navigate": "browser_navigate",
                }
                arguments = {
                    "target_id": str(params["target_id"]),
                    "tab_id": str(params["tab_id"]),
                    "session": self.agent_session,
                }
                for name in ("ref", "text", "url", "x", "y"):
                    if name in params:
                        arguments[name] = params[name]
                if tool == "click":
                    arguments["input_route"] = "dom_event"
                if tool == "type_text":
                    arguments["mode"] = "insert_text"
                result = await self.driver.call_tool(
                    browser_tools[tool],
                    json.dumps(arguments, separators=(",", ":")),
                )
                self._require_success(result)
                return json.loads(result.raw_json)
            arguments = {
                "pid": int(params["pid"]),
                "window_id": int(params["window_id"]),
                "delivery_mode": "foreground",
                "session": self.agent_session,
            }
            for name in (
                "element_index",
                "element_token",
                "x",
                "y",
                "button",
                "count",
                "text",
                "key",
                "modifiers",
                "keys",
                "direction",
                "amount",
            ):
                if name in params:
                    arguments[name] = params[name]
            if tool == "scroll":
                arguments["by"] = "line"
            result = await self.driver.call_tool(
                tool,
                json.dumps(arguments, separators=(",", ":")),
            )
            self._require_success(result)
            return json.loads(result.raw_json)
        if method == "move":
            result = await self.driver.move_cursor(
                MoveCursorInput(
                    x=float(params["x"]),
                    y=float(params["y"]),
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                )
            )
        elif method == "click":
            buttons = {
                "left": ClickButton.LEFT,
                "right": ClickButton.RIGHT,
                "middle": ClickButton.MIDDLE,
            }
            result = await self.driver.click(
                ClickInput(
                    x=float(params["x"]),
                    y=float(params["y"]),
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                    button=buttons[params.get("button", "left")],
                    count=int(params.get("count", 1)),
                )
            )
        elif method == "type":
            result = await self.driver.type_text(
                TypeTextInput(
                    text=str(params["text"]),
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                )
            )
        elif method == "key":
            result = await self.driver.press_key(
                PressKeyInput(
                    key=str(params["key"]),
                    modifiers=list(params.get("modifiers", [])) or None,
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                )
            )
        elif method == "hotkey":
            result = await self.driver.hotkey(
                HotkeyInput(
                    keys=list(params["keys"]),
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                )
            )
        elif method == "scroll":
            directions = {
                "up": ScrollDirection.UP,
                "down": ScrollDirection.DOWN,
                "left": ScrollDirection.LEFT,
                "right": ScrollDirection.RIGHT,
            }
            result = await self.driver.scroll(
                ScrollInput(
                    x=float(params["x"]),
                    y=float(params["y"]),
                    direction=directions[params["direction"]],
                    scope=DesktopScope.DESKTOP,
                    session=self.session,
                    by=ScrollBy.LINE,
                    amount=int(params.get("amount", 1)),
                )
            )
        else:
            raise ValueError("unsupported method")
        self._require_success(result)
        return {"verified": result.verified, "degraded": result.degraded}

    @staticmethod
    def _require_success(result) -> None:
        if result.is_error:
            raise RuntimeError(result.error_code or "Cua operation failed")
        value = json.loads(result.raw_json)
        structured = value.get("structuredContent", {})
        if structured.get("status") == "refused":
            refusal = structured.get("refusal", {})
            raise RuntimeError(
                str(refusal.get("code", "Cua operation refused"))
            )

    @classmethod
    def _bound(cls, value):
        if isinstance(value, str):
            return value[:32768]
        if isinstance(value, list):
            return [cls._bound(item) for item in value[:200]]
        if isinstance(value, dict):
            return {
                str(key)[:128]: cls._bound(item)
                for key, item in list(value.items())[:200]
            }
        return value


async def main() -> None:
    bridge = Bridge()
    await bridge.start()
    try:
        while True:
            line = await asyncio.to_thread(sys.stdin.readline)
            if line == "":
                break
            request_id = None
            try:
                request = json.loads(line)
                request_id = request["id"]
                result = await bridge.dispatch(
                    str(request["method"]),
                    dict(request.get("params", {})),
                )
                response = {"id": request_id, "ok": True, "result": result}
            except Exception as error:
                response = {
                    "id": request_id,
                    "ok": False,
                    "error": str(error)[:512],
                }
            sys.stdout.write(json.dumps(response, separators=(",", ":")) + "\n")
            sys.stdout.flush()
    finally:
        await bridge.close()


if __name__ == "__main__":
    asyncio.run(main())
