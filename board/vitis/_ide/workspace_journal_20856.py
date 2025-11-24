# 2025-11-23T23:53:41.192501600
import vitis

client = vitis.create_client()
client.set_workspace(path="vitis")

platform = client.get_component(name="zynq_platform")
status = platform.build()

comp = client.get_component(name="ethernet-receiver")
comp.build()

status = platform.build()

comp.build()

status = platform.build()

comp.build()

status = platform.build()

comp.build()

status = platform.build()

status = platform.build()

comp.build()

vitis.dispose()

