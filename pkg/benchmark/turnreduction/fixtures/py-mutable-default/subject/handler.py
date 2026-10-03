def handle_request(data, tags=[]):
    tags.append("processed")
    return {"data": data, "tags": tags}
