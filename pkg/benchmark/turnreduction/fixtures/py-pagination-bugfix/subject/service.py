def get_total_pages(total_items, per_page):
    if total_items == 0:
        return 0
    return total_items // per_page
    
def paginate(items, page, per_page):
    start = (page - 1) * per_page
    end = start + per_page
    return {
        "items": items[start:end],
        "total_pages": get_total_pages(len(items), per_page)
    }
