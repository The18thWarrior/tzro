"""Simple API client for the weather service."""

import urllib.request
import json


class WeatherClient:
    """Client for fetching weather data from the weather API."""

    def __init__(self, base_url: str, api_key: str):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key

    def get_current(self, city: str) -> dict:
        """Fetch current weather for a city."""
        url = f"{self.base_url}/current?city={city}&key={self.api_key}"
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read().decode())

    def get_forecast(self, city: str, days: int = 5) -> list[dict]:
        """Fetch weather forecast for a city."""
        url = f"{self.base_url}/forecast?city={city}&days={days}&key={self.api_key}"
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode())
            return data["forecast"]

    def get_alerts(self, city: str) -> list[dict]:
        """Fetch active weather alerts for a city."""
        url = f"{self.base_url}/alerts?city={city}&key={self.api_key}"
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode())
            return data.get("alerts", [])
