from service import process_order

def handle_cli_order(order_id, qty):
    return process_order(order_id, qty=qty)
