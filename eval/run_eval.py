import argparse
import csv
import hashlib
import json
import os
import statistics
import sys
import time
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path
 
import httpx
from dotenv import load_dotenv
 
load_dotenv()
 
BASE_URL = os.getenv("NIM_BASE_URL", "https://integrate.api.nvidia.com/v1").rstrip("/")
API_KEY = os.getenv("NVIDIA_API_KEY")
TIMEOUT = float(os.getenv("NIM_TIMEOUT", "90"))