"""Select the owned Heleket fixture; reuse the verified crypto acceptance flow."""
import os
from pathlib import Path
import runpy

os.environ['LOCAL_CRYPTO_PROVIDER'] = 'heleket'
runpy.run_path(Path(__file__).with_name('cryptomus-local.py'), run_name='__main__')
